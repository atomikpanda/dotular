package actions

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/atomikpanda/dotular/internal/color"
)

type packageState uint8

const (
	packageStateUnknown packageState = iota
	packageStateAbsent
	packageStatePresent
)

// PackageAction installs a package via the specified package manager.
//
// Idempotency: PackageAction implements Idempotent. IsApplied queries the
// package manager (e.g. `brew list`, `winget list`) to check whether the
// package is already installed. If the check command is unavailable the
// query is skipped and the install proceeds normally.
type PackageAction struct {
	Package string
	Manager string // e.g. "brew", "winget", "apt"

	executor commandExecutor
}

func (a *PackageAction) Describe() string {
	return fmt.Sprintf("install package %q via %s", a.Package, a.Manager)
}

func (a *PackageAction) Run(ctx context.Context, dryRun bool) error {
	args, err := installArgs(a.Manager, a.Package)
	if err != nil {
		return err
	}
	if dryRun {
		fmt.Printf("    %s\n", color.Dim(fmt.Sprintf("[dry-run] %s %s", args[0], strings.Join(args[1:], " "))))
		return nil
	}
	_, err = a.commandExecutor()(ctx, args, false)
	return err
}

// IsApplied uses the manager's per-package exit-status check except for mas,
// whose inventory command succeeds even when the requested package is absent.
// Rollback capture is otherwise intentionally independent because its stricter
// inventory parsing may be unable to authorize an automatic uninstall.
func (a *PackageAction) IsApplied(ctx context.Context) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	if a.Manager == "mas" {
		state, _, err := a.captureState(ctx)
		if err != nil {
			return false, err
		}
		return state == packageStatePresent, nil
	}
	args := CheckArgs(a.Manager, a.Package)
	if args == nil {
		return false, nil
	}

	_, err := a.commandExecutor()(ctx, args, true)
	if ctxErr := ctx.Err(); ctxErr != nil {
		return false, ctxErr
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return false, err
	}
	return err == nil, nil
}

// PrepareCompensation captures whether this exact package was installed before
// execution. Only conclusive absence produces an automatic uninstall.
func (a *PackageAction) PrepareCompensation(ctx context.Context) (CompensationPreparation, error) {
	state, unavailableReason, err := a.captureState(ctx)
	if err != nil {
		return CompensationPreparation{}, err
	}

	switch state {
	case packageStatePresent:
		return CompensationPreparation{AlreadyApplied: true}, nil
	case packageStateAbsent:
		args, err := uninstallArgs(a.Manager, a.Package)
		if err != nil {
			return CompensationPreparation{UnavailableReason: err.Error()}, nil
		}
		return CompensationPreparation{Compensation: &packageCompensation{
			manager:  a.Manager,
			pkg:      a.Package,
			args:     args,
			executor: a.commandExecutor(),
		}}, nil
	default:
		return CompensationPreparation{UnavailableReason: unavailableReason}, nil
	}
}

func (a *PackageAction) captureState(ctx context.Context) (packageState, string, error) {
	if err := ctx.Err(); err != nil {
		return packageStateUnknown, "", err
	}
	args := packageStateArgs(a.Manager, a.Package)
	if args == nil {
		if a.Manager == "nix" {
			return packageStateUnknown, "nix package attributes do not map exactly to installed package names", nil
		}
		return packageStateUnknown, fmt.Sprintf("package state query is unsupported for manager %q", a.Manager), nil
	}

	output, err := a.commandExecutor()(ctx, args, true)
	if ctxErr := ctx.Err(); ctxErr != nil {
		return packageStateUnknown, "", ctxErr
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return packageStateUnknown, "", err
	}
	if err != nil {
		return packageStateUnknown, fmt.Sprintf("package state query %q failed: %v", args, err), nil
	}

	state, err := parsePackageState(a.Manager, a.Package, output)
	if err != nil {
		return packageStateUnknown, fmt.Sprintf("package state query %q could not establish exact state: %v", args, err), nil
	}
	if a.Manager == "pacman" && state == packageStateAbsent {
		identityArgs := []string{"pacman", "-Slq"}
		identityOutput, identityErr := a.commandExecutor()(ctx, identityArgs, true)
		if ctxErr := ctx.Err(); ctxErr != nil {
			return packageStateUnknown, "", ctxErr
		}
		if errors.Is(identityErr, context.Canceled) || errors.Is(identityErr, context.DeadlineExceeded) {
			return packageStateUnknown, "", identityErr
		}
		if identityErr != nil {
			return packageStateUnknown, fmt.Sprintf("package identity query %q failed: %v", identityArgs, identityErr), nil
		}
		identityState, parseErr := parsePacmanState(a.Package, string(identityOutput))
		if parseErr != nil {
			return packageStateUnknown, fmt.Sprintf("package identity query %q could not establish exact state: %v", identityArgs, parseErr), nil
		}
		if identityState != packageStatePresent {
			return packageStateUnknown, fmt.Sprintf("pacman target %q is not an exact sync package identity", a.Package), nil
		}
	}
	return state, "", nil
}

func (a *PackageAction) commandExecutor() commandExecutor {
	if a.executor != nil {
		return a.executor
	}
	return executePackageCommand
}

type packageCompensation struct {
	manager  string
	pkg      string
	args     []string
	executor commandExecutor
}

func (c *packageCompensation) Describe() string {
	return fmt.Sprintf("uninstall package %q via %s", c.pkg, c.manager)
}

func (c *packageCompensation) Run(ctx context.Context) error {
	if _, err := c.executor(ctx, c.args, false); err != nil {
		return fmt.Errorf("package uninstall %q failed: %w", c.args, err)
	}
	return nil
}

func executePackageCommand(ctx context.Context, args []string, captureOutput bool) ([]byte, error) {
	output, err := executeCommand(ctx, args, captureOutput)
	if err != nil {
		return output, fmt.Errorf("package command %q: %w", args, err)
	}
	return output, nil
}

// CheckArgs returns a per-package check whose exit status indicates whether pkg
// is installed. Callers that need exact state capture use packageStateArgs.
func CheckArgs(manager, pkg string) []string {
	switch manager {
	case "brew":
		return []string{"brew", "list", "--formula", pkg}
	case "brew-cask":
		return []string{"brew", "list", "--cask", pkg}
	case "mas":
		return []string{"mas", "list"}
	case "winget":
		return []string{"winget", "list", "--id", pkg, "-e"}
	case "choco":
		return []string{"choco", "list", "--local-only", pkg}
	case "scoop":
		return []string{"scoop", "info", pkg}
	case "apt", "apt-get":
		return []string{"dpkg", "-s", pkg}
	case "dnf", "yum":
		return []string{"rpm", "-q", pkg}
	case "pacman":
		return []string{"pacman", "-Q", pkg}
	case "snap":
		return []string{"snap", "list", pkg}
	case "flatpak":
		return []string{"flatpak", "info", pkg}
	case "nix":
		return []string{"nix-env", "-q", pkg}
	default:
		return nil
	}
}

func packageStateArgs(manager, pkg string) []string {
	switch manager {
	case "brew":
		return []string{"brew", "list", "--formula", "--full-name"}
	case "brew-cask":
		return []string{"brew", "list", "--cask", "--full-name"}
	case "mas":
		return []string{"mas", "list"}
	case "winget":
		return []string{"winget", "list", "--disable-interactivity"}
	case "choco":
		return []string{"choco", "list", "--local-only", "--limit-output"}
	case "scoop":
		return []string{"scoop", "export"}
	case "apt", "apt-get":
		return []string{"dpkg-query", "-W", "-f=${binary:Package}\t${db:Status-Abbrev}\n"}
	case "dnf", "yum":
		return []string{"rpm", "-qa", "--qf", "%{NAME}\t%{EPOCH}\t%{VERSION}\t%{RELEASE}\t%{ARCH}\t[%{PROVIDENAME},]\n"}
	case "pacman":
		return []string{"pacman", "-Qq"}
	case "snap":
		return []string{"snap", "list"}
	case "flatpak":
		return []string{"flatpak", "list", "--columns=application,arch,branch"}
	default:
		return nil
	}
}

func parsePackageState(manager, pkg string, output []byte) (packageState, error) {
	if !utf8.Valid(output) {
		return packageStateUnknown, errors.New("output is not valid UTF-8")
	}

	switch manager {
	case "brew", "brew-cask":
		return parseBrewState(pkg, string(output))
	case "pacman":
		return parsePacmanState(pkg, string(output))
	case "mas":
		return parseMASState(pkg, string(output))
	case "winget":
		return parseWingetState(pkg, string(output))
	case "choco":
		return parseChocoState(pkg, string(output))
	case "scoop":
		return parseScoopState(pkg, output)
	case "apt", "apt-get":
		return parseDPKGState(pkg, string(output))
	case "dnf", "yum":
		return parseRPMState(pkg, string(output))
	case "snap":
		return parseSnapState(pkg, string(output))
	case "flatpak":
		return parseFlatpakState(pkg, string(output))
	default:
		return packageStateUnknown, fmt.Errorf("unsupported package manager %q", manager)
	}
}

func parseBrewState(pkg, output string) (packageState, error) {
	if pkg == "" || strings.HasSuffix(pkg, "/") {
		return packageStateUnknown, fmt.Errorf("invalid Homebrew package identity %q", pkg)
	}
	shortName := pkg
	if separator := strings.LastIndex(pkg, "/"); separator >= 0 {
		shortName = pkg[separator+1:]
	}
	ambiguousName := ""
	for _, line := range outputLines(output) {
		fields := strings.Fields(line)
		if len(fields) != 1 {
			return packageStateUnknown, fmt.Errorf("invalid Homebrew package identity line %q", line)
		}
		installedName := fields[0]
		if installedName == pkg {
			return packageStatePresent, nil
		}
		installedShortName := installedName
		if separator := strings.LastIndex(installedName, "/"); separator >= 0 {
			installedShortName = installedName[separator+1:]
		}
		if installedShortName == shortName ||
			strings.TrimSuffix(installedShortName, versionSuffix(installedShortName)) ==
				strings.TrimSuffix(shortName, versionSuffix(shortName)) {
			ambiguousName = installedName
		}
	}
	if ambiguousName != "" {
		return packageStateUnknown, fmt.Errorf("Homebrew identity %q may refer to installed package %q", pkg, ambiguousName)
	}
	return packageStateAbsent, nil
}

func parsePacmanState(pkg, output string) (packageState, error) {
	if pkg == "" || strings.Contains(pkg, "/") || strings.ContainsAny(pkg, " \t\r\n") {
		return packageStateUnknown, fmt.Errorf("pacman target %q cannot be mapped to one package identity", pkg)
	}
	for _, line := range outputLines(output) {
		fields := strings.Fields(line)
		if len(fields) != 1 {
			return packageStateUnknown, fmt.Errorf("invalid pacman package identity line %q", line)
		}
		if fields[0] == pkg {
			return packageStatePresent, nil
		}
	}
	return packageStateAbsent, nil
}

func versionSuffix(value string) string {
	if separator := strings.LastIndex(value, "@"); separator >= 0 {
		return value[separator:]
	}
	return ""
}

func parseMASState(pkg, output string) (packageState, error) {
	targetID, err := strconv.ParseUint(pkg, 10, 64)
	if err != nil {
		return packageStateUnknown, fmt.Errorf("invalid mas package ID %q", pkg)
	}
	for _, line := range outputLines(output) {
		fields := strings.Fields(line)
		if len(fields) < 2 {
			return packageStateUnknown, fmt.Errorf("invalid mas list row %q", line)
		}
		installedID, err := strconv.ParseUint(fields[0], 10, 64)
		if err != nil {
			return packageStateUnknown, fmt.Errorf("invalid mas package ID in row %q", line)
		}
		if installedID == targetID {
			return packageStatePresent, nil
		}
	}
	return packageStateAbsent, nil
}

var wingetColumnSeparator = regexp.MustCompile(`[ \t]{2,}`)

func parseWingetState(pkg, output string) (packageState, error) {
	lines := outputLines(output)
	for index, header := range lines {
		headerColumns := wingetColumnSeparator.Split(strings.TrimSpace(header), -1)
		if len(headerColumns) < 3 || headerColumns[0] != "Name" {
			continue
		}
		idColumn := -1
		for column, name := range headerColumns {
			if name == "Id" {
				idColumn = column
				break
			}
		}
		if idColumn < 0 {
			continue
		}
		if index+1 >= len(lines) || !isDashSeparator(lines[index+1]) {
			return packageStateUnknown, errors.New("winget table header has no separator")
		}
		truncatedID := ""
		for _, row := range lines[index+2:] {
			columns := wingetColumnSeparator.Split(strings.TrimSpace(row), -1)
			if len(columns) != len(headerColumns) {
				return packageStateUnknown, fmt.Errorf("invalid winget list row %q", row)
			}
			installedID := columns[idColumn]
			if strings.Contains(installedID, "…") || strings.HasSuffix(installedID, "...") {
				truncatedID = installedID
				continue
			}
			if strings.EqualFold(installedID, pkg) {
				return packageStatePresent, nil
			}
		}
		if truncatedID != "" {
			return packageStateUnknown, fmt.Errorf("truncated winget ID %q prevents exact absence", truncatedID)
		}
		return packageStateAbsent, nil
	}
	return packageStateUnknown, errors.New("winget package table not found")
}

func parseChocoState(pkg, output string) (packageState, error) {
	for _, line := range outputLines(output) {
		fields := strings.SplitN(line, "|", 2)
		if len(fields) != 2 || fields[0] == "" || fields[1] == "" {
			return packageStateUnknown, fmt.Errorf("invalid choco list row %q", line)
		}
		if strings.EqualFold(fields[0], pkg) {
			return packageStatePresent, nil
		}
	}
	return packageStateAbsent, nil
}

func parseScoopState(pkg string, output []byte) (packageState, error) {
	var exported struct {
		Apps *[]struct {
			Name   string
			Source string
		}
	}
	if err := json.Unmarshal(output, &exported); err != nil {
		return packageStateUnknown, err
	}
	if exported.Apps == nil {
		return packageStateUnknown, errors.New("scoop export has no apps list")
	}

	targetSource, targetName := "", pkg
	if strings.Contains(pkg, "/") {
		fields := strings.Split(pkg, "/")
		if len(fields) != 2 || fields[0] == "" || fields[1] == "" {
			return packageStateUnknown, fmt.Errorf("scoop identity %q cannot be mapped exactly", pkg)
		}
		targetSource, targetName = fields[0], fields[1]
	}
	ambiguousSourceFound := false
	ambiguousSource := ""
	for _, app := range *exported.Apps {
		if app.Name == "" {
			return packageStateUnknown, errors.New("scoop export contains an app without a name")
		}
		if !strings.EqualFold(app.Name, targetName) {
			continue
		}
		if targetSource == "" {
			return packageStatePresent, nil
		}
		if app.Source != "" && strings.EqualFold(app.Source, targetSource) {
			return packageStatePresent, nil
		}
		ambiguousSourceFound = true
		ambiguousSource = app.Source
	}
	if ambiguousSourceFound {
		return packageStateUnknown, fmt.Errorf("scoop app %q has source %q, not exact requested source %q", targetName, ambiguousSource, targetSource)
	}
	return packageStateAbsent, nil
}

var (
	debianPackageNamePattern  = regexp.MustCompile(`^[a-z0-9][a-z0-9+.-]*$`)
	debianArchitecturePattern = regexp.MustCompile(`^[a-z0-9][a-z0-9-]*$`)
)

func parseDPKGState(pkg, output string) (packageState, error) {
	targetName, targetArchitecture, err := parseDPKGIdentity(pkg, true)
	if err != nil {
		return packageStateUnknown, err
	}
	ambiguousInstalledPackage := ""
	for _, line := range outputLines(output) {
		fields := strings.Split(line, "\t")
		if len(fields) != 2 || fields[0] == "" || len(fields[1]) != 3 {
			return packageStateUnknown, fmt.Errorf("invalid dpkg-query row %q", line)
		}
		status := fields[1]
		if !strings.ContainsRune("uihrp", rune(status[0])) ||
			!strings.ContainsRune("ncHUFWti", rune(status[1])) ||
			!strings.ContainsRune(" R", rune(status[2])) {
			return packageStateUnknown, fmt.Errorf("invalid dpkg status in row %q", line)
		}

		installedName, installedArchitecture, err := parseDPKGIdentity(fields[0], false)
		if err != nil {
			return packageStateUnknown, fmt.Errorf("invalid dpkg package identity in row %q: %w", line, err)
		}
		sameName := installedName == targetName
		exactTarget := sameName && targetArchitecture == installedArchitecture
		architectureOmitted := sameName && targetArchitecture != installedArchitecture &&
			(targetArchitecture == "" || installedArchitecture == "")
		if exactTarget {
			if status[2] == 'R' {
				return packageStateUnknown, fmt.Errorf("package %q requires reinstallation", fields[0])
			}
			switch status[1] {
			case 'i':
				return packageStatePresent, nil
			case 'H', 'U', 'F', 'W', 't':
				return packageStateUnknown, fmt.Errorf("package %q has transitional dpkg status %q", fields[0], status)
			}
			continue
		}
		if architectureOmitted && status[1] != 'n' && status[1] != 'c' {
			ambiguousInstalledPackage = fields[0]
		}
		if status[1] == 'i' && targetArchitecture == "" &&
			strings.HasPrefix(installedName, targetName+"-") &&
			isKnownDebianArchitecture(strings.TrimPrefix(installedName, targetName+"-")) {
			ambiguousInstalledPackage = fields[0]
		}
	}
	if ambiguousInstalledPackage != "" {
		return packageStateUnknown, fmt.Errorf("installed package %q may map to %q but cannot be identified exactly", ambiguousInstalledPackage, pkg)
	}
	return packageStateUnknown, fmt.Errorf("dpkg inventory cannot prove %q absent because apt may resolve it through a virtual provider", pkg)
}

func parseDPKGIdentity(identity string, rejectSelectors bool) (name, architecture string, err error) {
	if rejectSelectors && strings.ContainsAny(identity, "*?[]=/\\") {
		return "", "", fmt.Errorf("apt package selector %q cannot be mapped exactly", identity)
	}
	if rejectSelectors && (strings.HasSuffix(identity, "+") || strings.HasSuffix(identity, "-")) {
		return "", "", fmt.Errorf("apt action suffix in %q cannot be mapped to a package identity", identity)
	}
	fields := strings.Split(identity, ":")
	if len(fields) > 2 {
		return "", "", fmt.Errorf("invalid apt package identity %q", identity)
	}
	name = fields[0]
	if !debianPackageNamePattern.MatchString(name) {
		return "", "", fmt.Errorf("invalid apt package name %q", name)
	}
	if len(fields) == 1 {
		return name, "", nil
	}
	architecture = fields[1]
	if !debianArchitecturePattern.MatchString(architecture) {
		return "", "", fmt.Errorf("invalid apt package architecture %q", architecture)
	}
	if rejectSelectors && (architecture == "any" || architecture == "native") {
		return "", "", fmt.Errorf("apt architecture selector %q cannot be mapped exactly", identity)
	}
	return name, architecture, nil
}

func isKnownDebianArchitecture(value string) bool {
	switch value {
	case "amd64", "arm64", "armel", "armhf", "i386", "mips64el", "mipsel", "ppc64el", "riscv64", "s390x":
		return true
	default:
		return false
	}
}

func parseRPMState(pkg, output string) (packageState, error) {
	if pkg == "" || strings.ContainsAny(pkg, "*?[]/()<>@='\" \t\r\n:") {
		return packageStateUnknown, fmt.Errorf("rpm package selector %q cannot be mapped exactly", pkg)
	}
	versionRelatedPackage := ""
	providingPackage := ""
	architectureAmbiguousPackage := ""
	for _, line := range outputLines(output) {
		fields := strings.Split(line, "\t")
		if len(fields) != 6 {
			return packageStateUnknown, fmt.Errorf("invalid rpm query row %q", line)
		}
		for _, field := range fields[:5] {
			if field == "" || strings.ContainsAny(field, " \t") {
				return packageStateUnknown, fmt.Errorf("invalid rpm query row %q", line)
			}
		}

		name, version, release, architecture := fields[0], fields[2], fields[3], fields[4]
		if pkg == name+"."+architecture {
			return packageStatePresent, nil
		}
		if pkg == name {
			if architecture == "noarch" {
				return packageStatePresent, nil
			}
			architectureAmbiguousPackage = name + "." + architecture
		}
		for _, provided := range strings.Split(fields[5], ",") {
			if strings.TrimSpace(provided) == pkg && pkg != name {
				providingPackage = name + "." + architecture
			}
		}

		versionPrefix := name + "-"
		if strings.HasPrefix(pkg, versionPrefix) {
			queriedNEVR := name + "-" + version + "-" + release
			versionRelatedPackage = queriedNEVR + "." + architecture
		}
	}
	switch {
	case versionRelatedPackage != "":
		return packageStateUnknown, fmt.Errorf("installed package %q is related to version-like selector %q", versionRelatedPackage, pkg)
	case providingPackage != "":
		return packageStateUnknown, fmt.Errorf("installed package %q provides %q but has no exact inverse", providingPackage, pkg)
	case architectureAmbiguousPackage != "":
		return packageStateUnknown, fmt.Errorf("installed package %q may be foreign architecture for bare target %q", architectureAmbiguousPackage, pkg)
	default:
		return packageStateUnknown, fmt.Errorf("rpm inventory cannot prove %q absent because the install target may resolve through a virtual provider", pkg)
	}
}

func parseFlatpakState(pkg, output string) (packageState, error) {
	app, architecture, branch, err := parseFlatpakTarget(pkg)
	if err != nil {
		return packageStateUnknown, err
	}
	ambiguousArchitecture := ""
	ambiguousBranch := ""
	for _, line := range outputLines(output) {
		fields := strings.Split(line, "\t")
		if len(fields) != 3 || fields[0] == "" || fields[1] == "" || fields[2] == "" {
			return packageStateUnknown, fmt.Errorf("invalid flatpak list row %q", line)
		}
		if fields[0] != app {
			continue
		}
		if architecture == "" {
			if branch == "" || fields[2] == branch {
				ambiguousArchitecture = fields[1]
			}
			continue
		}
		if fields[1] != architecture {
			continue
		}
		if branch != "" {
			if fields[2] == branch {
				return packageStatePresent, nil
			}
			continue
		}
		if fields[2] == "stable" {
			return packageStatePresent, nil
		}
		ambiguousBranch = fields[2]
	}
	if ambiguousArchitecture != "" {
		return packageStateUnknown, fmt.Errorf("flatpak ref %q omits architecture but may map to installed architecture %q", pkg, ambiguousArchitecture)
	}
	if ambiguousBranch != "" {
		return packageStateUnknown, fmt.Errorf("partial flatpak ref %q may map to installed nondefault branch %q", pkg, ambiguousBranch)
	}
	return packageStateAbsent, nil
}

func parseFlatpakTarget(pkg string) (app, architecture, branch string, err error) {
	if strings.Contains(pkg, ":") {
		return "", "", "", fmt.Errorf("remote-qualified flatpak ref %q cannot be mapped exactly", pkg)
	}
	appID := pkg
	if separator := strings.Index(appID, "//"); separator >= 0 {
		appID = appID[:separator]
	} else if separator := strings.Index(appID, "/"); separator >= 0 {
		appID = appID[:separator]
	}
	if !isCanonicalFlatpakID(appID) {
		return "", "", "", fmt.Errorf("fuzzy flatpak identity %q cannot be mapped exactly", pkg)
	}
	if strings.Contains(pkg, "//") {
		fields := strings.Split(pkg, "//")
		if len(fields) != 2 || fields[0] == "" || fields[1] == "" || strings.Contains(fields[0], "/") || strings.Contains(fields[1], "/") {
			return "", "", "", fmt.Errorf("invalid branch-qualified flatpak ref %q", pkg)
		}
		return fields[0], "", fields[1], nil
	}
	fields := strings.Split(pkg, "/")
	switch len(fields) {
	case 1:
		if fields[0] == "" {
			return "", "", "", errors.New("empty flatpak application ID")
		}
		return fields[0], "", "", nil
	case 2:
		if fields[0] == "" || fields[1] == "" {
			return "", "", "", fmt.Errorf("invalid architecture-qualified flatpak ref %q", pkg)
		}
		return fields[0], fields[1], "", nil
	case 3:
		if fields[0] == "" || fields[1] == "" || fields[2] == "" {
			return "", "", "", fmt.Errorf("invalid flatpak ref %q", pkg)
		}
		return fields[0], fields[1], fields[2], nil
	default:
		return "", "", "", fmt.Errorf("flatpak ref %q cannot be mapped exactly", pkg)
	}
}

func isCanonicalFlatpakID(value string) bool {
	components := strings.Split(value, ".")
	if len(components) < 3 {
		return false
	}
	for _, component := range components {
		if component == "" {
			return false
		}
		for _, character := range component {
			if (character < 'a' || character > 'z') &&
				(character < 'A' || character > 'Z') &&
				(character < '0' || character > '9') &&
				character != '_' && character != '-' {
				return false
			}
		}
	}
	return true
}

func parseSnapState(pkg, output string) (packageState, error) {
	lines := outputLines(output)
	if len(lines) == 0 {
		return packageStateUnknown, errors.New("snap list has no header")
	}
	if len(lines) == 1 && strings.HasPrefix(lines[0], "No snaps are installed") {
		return packageStateAbsent, nil
	}
	header := strings.Fields(lines[0])
	if len(header) < 2 || header[0] != "Name" || header[1] != "Version" {
		return packageStateUnknown, errors.New("snap list header not found")
	}
	for _, line := range lines[1:] {
		fields := strings.Fields(line)
		if len(fields) < 2 {
			return packageStateUnknown, fmt.Errorf("invalid snap list row %q", line)
		}
		if fields[0] == pkg {
			return packageStatePresent, nil
		}
	}
	return packageStateAbsent, nil
}

func outputLines(output string) []string {
	rawLines := strings.Split(strings.ReplaceAll(output, "\r\n", "\n"), "\n")
	lines := make([]string, 0, len(rawLines))
	for _, line := range rawLines {
		if strings.TrimSpace(line) != "" {
			lines = append(lines, line)
		}
	}
	return lines
}

func isDashSeparator(line string) bool {
	trimmed := strings.TrimSpace(line)
	return trimmed != "" && strings.Trim(trimmed, "-") == ""
}

// installArgs returns the command + arguments needed to install pkg.
func installArgs(manager, pkg string) ([]string, error) {
	switch manager {
	case "brew":
		return []string{"brew", "install", pkg}, nil
	case "brew-cask":
		return []string{"brew", "install", "--cask", pkg}, nil
	case "mas":
		return []string{"mas", "install", pkg}, nil
	case "winget":
		return []string{"winget", "install", "--id", pkg, "-e", "--accept-source-agreements"}, nil
	case "choco":
		return []string{"choco", "install", pkg, "-y"}, nil
	case "scoop":
		return []string{"scoop", "install", pkg}, nil
	case "apt", "apt-get":
		return []string{"sudo", "apt-get", "install", "-y", pkg}, nil
	case "dnf":
		return []string{"sudo", "dnf", "install", "-y", pkg}, nil
	case "yum":
		return []string{"sudo", "yum", "install", "-y", pkg}, nil
	case "pacman":
		return []string{"sudo", "pacman", "-S", "--noconfirm", pkg}, nil
	case "snap":
		return []string{"sudo", "snap", "install", pkg}, nil
	case "flatpak":
		return []string{"flatpak", "install", "-y", pkg}, nil
	case "nix":
		return []string{"nix-env", "-iA", pkg}, nil
	default:
		return nil, fmt.Errorf("unknown package manager: %q", manager)
	}
}

func uninstallArgs(manager, pkg string) ([]string, error) {
	switch manager {
	case "brew":
		return []string{"brew", "uninstall", pkg}, nil
	case "brew-cask":
		return []string{"brew", "uninstall", "--cask", pkg}, nil
	case "mas":
		return []string{"mas", "uninstall", pkg}, nil
	case "winget":
		return []string{"winget", "uninstall", "--id", pkg, "-e", "--disable-interactivity"}, nil
	case "choco":
		return []string{"choco", "uninstall", pkg, "-y"}, nil
	case "scoop":
		return []string{"scoop", "uninstall", pkg}, nil
	case "apt", "apt-get":
		return []string{"sudo", "apt-get", "remove", "-y", pkg}, nil
	case "dnf":
		return []string{"sudo", "dnf", "remove", "-y", pkg}, nil
	case "yum":
		return []string{"sudo", "yum", "remove", "-y", pkg}, nil
	case "pacman":
		return []string{"sudo", "pacman", "-R", "--noconfirm", pkg}, nil
	case "snap":
		return []string{"sudo", "snap", "remove", pkg}, nil
	case "flatpak":
		return []string{"flatpak", "uninstall", "-y", pkg}, nil
	case "nix":
		return nil, errors.New("nix package identity is imprecise; automatic uninstall is unavailable")
	default:
		return nil, fmt.Errorf("unknown package manager: %q", manager)
	}
}
