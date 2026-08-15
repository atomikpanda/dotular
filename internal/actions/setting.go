package actions

import (
	"context"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"unicode/utf16"
	"unicode/utf8"

	"github.com/atomikpanda/dotular/internal/color"
)

// SettingAction writes a system preference.
// On macOS it calls `defaults write`; on Windows it calls `reg add`.
type SettingAction struct {
	Domain string // macOS bundle ID or Windows registry path
	Key    string
	Value  any
	OS     string

	executor      commandExecutor
	windowsReader func(context.Context, string, string) (bool, uint32, []byte, error)
}

// SettingsSupported reports whether system settings can be applied on goos.
// macOS uses `defaults` and Windows uses `reg`; no other platform has a
// mechanism, so setting items are not applicable there.
func SettingsSupported(goos string) bool {
	return goos == "darwin" || goos == "windows"
}

func (a *SettingAction) Describe() string {
	return fmt.Sprintf("set %s %s = %v", a.Domain, a.Key, a.Value)
}

func (a *SettingAction) Run(ctx context.Context, dryRun bool) error {
	if dryRun {
		fmt.Printf("    %s\n", color.Dim(fmt.Sprintf("[dry-run] set: %s %s = %v", a.Domain, a.Key, a.Value)))
		return nil
	}

	var args []string
	switch a.operatingSystem() {
	case "darwin":
		typeFlag, value := macOSValueArgs(a.Value)
		args = []string{"defaults", "write", a.Domain, a.Key, typeFlag, value}
	case "windows":
		regType, value := windowsValueArgs(a.Value)
		args = []string{"reg", "add", a.Domain, "/v", a.Key, "/t", regType, "/d", value, "/f"}
	default:
		// Defensive: the runner skips setting items on unsupported platforms.
		return fmt.Errorf("system settings are not supported on %s", a.operatingSystem())
	}

	_, err := a.commandExecutor()(ctx, args, false)
	return err
}

type settingState uint8

const (
	settingStateUnknown settingState = iota
	settingStateAbsent
	settingStatePresent
)

type capturedSettingValue struct {
	typeArg string
	value   string
}

// PrepareCompensation captures the exact scalar setting state before Run.
// Only proven absence permits deletion; inconclusive state is left unavailable.
func (a *SettingAction) PrepareCompensation(ctx context.Context) (CompensationPreparation, error) {
	if err := ctx.Err(); err != nil {
		return CompensationPreparation{}, err
	}

	var (
		state             settingState
		captured          capturedSettingValue
		unavailableReason string
		err               error
	)
	switch a.operatingSystem() {
	case "darwin":
		state, captured, unavailableReason, err = a.captureMacOSState(ctx)
	case "windows":
		state, captured, unavailableReason, err = a.captureWindowsState(ctx)
	default:
		return CompensationPreparation{
			UnavailableReason: fmt.Sprintf("system setting state capture is unsupported on %s", a.operatingSystem()),
		}, nil
	}
	if err != nil {
		return CompensationPreparation{}, err
	}
	if state == settingStateUnknown {
		return CompensationPreparation{UnavailableReason: unavailableReason}, nil
	}

	compensation := &settingCompensation{
		domain:   a.Domain,
		key:      a.Key,
		executor: a.commandExecutor(),
	}
	switch a.operatingSystem() {
	case "darwin":
		if state == settingStateAbsent {
			compensation.delete = true
			compensation.args = []string{"defaults", "delete", a.Domain, a.Key}
		} else {
			compensation.args = []string{"defaults", "write", a.Domain, a.Key, captured.typeArg, captured.value}
		}
	case "windows":
		if state == settingStateAbsent {
			compensation.delete = true
			compensation.args = []string{"reg", "delete", a.Domain, "/v", a.Key, "/f"}
		} else {
			compensation.args = []string{"reg", "add", a.Domain, "/v", a.Key, "/t", captured.typeArg, "/d", captured.value, "/f"}
		}
	}
	return CompensationPreparation{Compensation: compensation}, nil
}

func (a *SettingAction) captureMacOSState(ctx context.Context) (settingState, capturedSettingValue, string, error) {
	typeArgs := []string{"defaults", "read-type", a.Domain, a.Key}
	typeOutput, err := a.commandExecutor()(ctx, typeArgs, true)
	if canceledErr := settingContextError(ctx, err); canceledErr != nil {
		return settingStateUnknown, capturedSettingValue{}, "", canceledErr
	}
	if err != nil {
		if macOSSettingMissing(err) {
			return settingStateAbsent, capturedSettingValue{}, "", nil
		}
		return settingStateUnknown, capturedSettingValue{}, settingQueryFailure(typeArgs, err), nil
	}

	typeFlag, ok := macOSRestoreType(string(typeOutput))
	if !ok {
		return settingStateUnknown, capturedSettingValue{}, fmt.Sprintf(
			"defaults read-type returned an unsupported or malformed type %q",
			strings.TrimSpace(string(typeOutput)),
		), nil
	}

	readArgs := []string{"defaults", "read", a.Domain, a.Key}
	valueOutput, err := a.commandExecutor()(ctx, readArgs, true)
	if canceledErr := settingContextError(ctx, err); canceledErr != nil {
		return settingStateUnknown, capturedSettingValue{}, "", canceledErr
	}
	if err != nil {
		return settingStateUnknown, capturedSettingValue{}, settingQueryFailure(readArgs, err), nil
	}
	if !utf8.Valid(valueOutput) {
		return settingStateUnknown, capturedSettingValue{}, "defaults read returned non-UTF-8 output", nil
	}

	return settingStatePresent, capturedSettingValue{
		typeArg: typeFlag,
		value:   trimCommandLineEnding(string(valueOutput)),
	}, "", nil
}

func (a *SettingAction) captureWindowsState(ctx context.Context) (settingState, capturedSettingValue, string, error) {
	present, valueType, data, err := a.windowsStateReader()(ctx, a.Domain, a.Key)
	if canceledErr := settingContextError(ctx, err); canceledErr != nil {
		return settingStateUnknown, capturedSettingValue{}, "", canceledErr
	}
	if err != nil {
		return settingStateUnknown, capturedSettingValue{}, fmt.Sprintf("native registry state query failed: %v", err), nil
	}
	if !present {
		return settingStateAbsent, capturedSettingValue{}, "", nil
	}

	regType, value, err := encodeWindowsRegistryValue(valueType, data)
	if err != nil {
		return settingStateUnknown, capturedSettingValue{}, fmt.Sprintf("native registry value cannot be restored exactly: %v", err), nil
	}
	return settingStatePresent, capturedSettingValue{typeArg: regType, value: value}, "", nil
}

func (a *SettingAction) operatingSystem() string {
	if a.OS != "" {
		return a.OS
	}
	return runtime.GOOS
}

func (a *SettingAction) commandExecutor() commandExecutor {
	if a.executor != nil {
		return a.executor
	}
	return executeCommand
}

func (a *SettingAction) windowsStateReader() func(context.Context, string, string) (bool, uint32, []byte, error) {
	if a.windowsReader != nil {
		return a.windowsReader
	}
	return readWindowsRegistryValue
}

type settingCompensation struct {
	domain   string
	key      string
	args     []string
	delete   bool
	executor commandExecutor
}

func (c *settingCompensation) Describe() string {
	if c.delete {
		return fmt.Sprintf("delete setting %s %s", c.domain, c.key)
	}
	return fmt.Sprintf("restore setting %s %s", c.domain, c.key)
}

func (c *settingCompensation) Run(ctx context.Context) error {
	if _, err := c.executor(ctx, c.args, false); err != nil {
		return fmt.Errorf("setting rollback %q failed: %w", c.args, err)
	}
	return nil
}

func macOSRestoreType(output string) (string, bool) {
	switch strings.TrimSpace(output) {
	case "Type is string":
		return "-string", true
	case "Type is boolean":
		return "-bool", true
	case "Type is integer":
		return "-int", true
	case "Type is float":
		return "-float", true
	default:
		return "", false
	}
}

const (
	windowsRegistryTypeString     = 1
	windowsRegistryTypeExpandText = 2
	windowsRegistryTypeBinary     = 3
	windowsRegistryTypeDWORD      = 4
	windowsRegistryTypeMultiText  = 7
	windowsRegistryTypeQWORD      = 11
)

func encodeWindowsRegistryValue(valueType uint32, data []byte) (string, string, error) {
	switch valueType {
	case windowsRegistryTypeString:
		value, err := decodeWindowsRegistryString(data)
		return "REG_SZ", value, err
	case windowsRegistryTypeExpandText:
		value, err := decodeWindowsRegistryString(data)
		return "REG_EXPAND_SZ", value, err
	case windowsRegistryTypeBinary:
		return "REG_BINARY", hex.EncodeToString(data), nil
	case windowsRegistryTypeDWORD:
		if len(data) != 4 {
			return "", "", fmt.Errorf("REG_DWORD data is %d bytes, want 4", len(data))
		}
		return "REG_DWORD", fmt.Sprintf("0x%08x", binary.LittleEndian.Uint32(data)), nil
	case windowsRegistryTypeMultiText:
		values, err := decodeWindowsRegistryMultiString(data)
		if err != nil {
			return "", "", err
		}
		return "REG_MULTI_SZ", strings.Join(values, `\0`), nil
	case windowsRegistryTypeQWORD:
		if len(data) != 8 {
			return "", "", fmt.Errorf("REG_QWORD data is %d bytes, want 8", len(data))
		}
		return "REG_QWORD", fmt.Sprintf("0x%016x", binary.LittleEndian.Uint64(data)), nil
	default:
		return "", "", fmt.Errorf("registry type %d is unsupported", valueType)
	}
}

func decodeWindowsRegistryString(data []byte) (string, error) {
	codeUnits, err := windowsRegistryCodeUnits(data)
	if err != nil {
		return "", err
	}
	if len(codeUnits) == 0 || codeUnits[len(codeUnits)-1] != 0 {
		return "", errors.New("registry string is not NUL-terminated")
	}
	codeUnits = codeUnits[:len(codeUnits)-1]
	for _, codeUnit := range codeUnits {
		if codeUnit == 0 {
			return "", errors.New("registry string contains an embedded NUL")
		}
	}
	return decodeWindowsRegistryUTF16(codeUnits)
}

func decodeWindowsRegistryMultiString(data []byte) ([]string, error) {
	codeUnits, err := windowsRegistryCodeUnits(data)
	if err != nil {
		return nil, err
	}
	if len(codeUnits) < 2 || codeUnits[len(codeUnits)-1] != 0 || codeUnits[len(codeUnits)-2] != 0 {
		return nil, errors.New("REG_MULTI_SZ is not double-NUL-terminated")
	}

	content := codeUnits[:len(codeUnits)-1]
	values := make([]string, 0)
	start := 0
	for i, codeUnit := range content {
		if codeUnit != 0 {
			continue
		}
		if i == start {
			return nil, errors.New("REG_MULTI_SZ contains an empty element")
		}
		value, err := decodeWindowsRegistryUTF16(content[start:i])
		if err != nil {
			return nil, err
		}
		if strings.Contains(value, `\0`) {
			return nil, errors.New(`REG_MULTI_SZ element contains the reg add separator \0`)
		}
		values = append(values, value)
		start = i + 1
	}
	if start != len(content) || len(values) == 0 {
		return nil, errors.New("REG_MULTI_SZ has malformed termination")
	}
	return values, nil
}

func windowsRegistryCodeUnits(data []byte) ([]uint16, error) {
	if len(data)%2 != 0 {
		return nil, errors.New("registry UTF-16 data has odd byte length")
	}
	codeUnits := make([]uint16, len(data)/2)
	for i := range codeUnits {
		codeUnits[i] = binary.LittleEndian.Uint16(data[i*2:])
	}
	return codeUnits, nil
}

func decodeWindowsRegistryUTF16(codeUnits []uint16) (string, error) {
	var value strings.Builder
	for i := 0; i < len(codeUnits); i++ {
		codeUnit := rune(codeUnits[i])
		switch {
		case codeUnit >= 0xd800 && codeUnit <= 0xdbff:
			if i+1 >= len(codeUnits) {
				return "", errors.New("registry string ends with an unpaired high surrogate")
			}
			low := rune(codeUnits[i+1])
			if low < 0xdc00 || low > 0xdfff {
				return "", errors.New("registry string contains an unpaired high surrogate")
			}
			value.WriteRune(utf16.DecodeRune(codeUnit, low))
			i++
		case codeUnit >= 0xdc00 && codeUnit <= 0xdfff:
			return "", errors.New("registry string contains an unpaired low surrogate")
		default:
			value.WriteRune(codeUnit)
		}
	}
	return value.String(), nil
}

func trimCommandLineEnding(value string) string {
	return strings.TrimSuffix(value, "\n")
}

func macOSSettingMissing(err error) bool {
	diagnostic := settingCommandDiagnostic(err)
	return strings.Contains(diagnostic, "The domain/default pair of (") &&
		strings.HasSuffix(diagnostic, ") does not exist")
}

func settingCommandDiagnostic(err error) string {
	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) {
		return ""
	}
	return strings.TrimSpace(string(exitErr.Stderr))
}

func settingContextError(ctx context.Context, err error) error {
	if ctxErr := ctx.Err(); ctxErr != nil {
		return ctxErr
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return err
	}
	return nil
}

func settingQueryFailure(args []string, err error) string {
	if diagnostic := settingCommandDiagnostic(err); diagnostic != "" {
		return fmt.Sprintf("setting state query %q failed: %s", args, diagnostic)
	}
	return fmt.Sprintf("setting state query %q failed: %v", args, err)
}

func windowsValueArgs(value any) (regType, regVal string) {
	switch v := value.(type) {
	case bool:
		if v {
			return "REG_DWORD", "1"
		}
		return "REG_DWORD", "0"
	case int:
		return "REG_DWORD", strconv.Itoa(v)
	case float64:
		return "REG_SZ", strconv.FormatFloat(v, 'f', -1, 64)
	case string:
		return "REG_SZ", v
	default:
		return "REG_SZ", fmt.Sprintf("%v", v)
	}
}

func macOSValueArgs(value any) (typeFlag, val string) {
	switch v := value.(type) {
	case bool:
		return "-bool", strconv.FormatBool(v)
	case int:
		return "-int", strconv.Itoa(v)
	case float64:
		return "-float", strconv.FormatFloat(v, 'f', -1, 64)
	case string:
		return "-string", v
	default:
		return "-string", fmt.Sprintf("%v", v)
	}
}
