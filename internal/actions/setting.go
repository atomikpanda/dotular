package actions

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strconv"
)

// SettingAction writes a system preference.
// On macOS it calls `defaults write`; Windows registry support is stubbed.
type SettingAction struct {
	Domain string // macOS bundle ID or Windows registry path
	Key    string
	Value  any
}

func (a *SettingAction) Describe() string {
	return fmt.Sprintf("set %s %s = %v", a.Domain, a.Key, a.Value)
}

func (a *SettingAction) Run(ctx context.Context, dryRun bool) error {
	if dryRun {
		fmt.Printf("    [dry-run] set: %s %s = %v\n", a.Domain, a.Key, a.Value)
		return nil
	}
	switch runtime.GOOS {
	case "darwin":
		return applyMacOSSetting(ctx, a.Domain, a.Key, a.Value)
	case "windows":
		return fmt.Errorf("Windows registry settings are not yet implemented")
	default:
		return fmt.Errorf("system settings are not supported on %s", runtime.GOOS)
	}
}

func applyMacOSSetting(ctx context.Context, domain, key string, value any) error {
	typeFlag, val := macOSValueArgs(value)
	cmd := exec.CommandContext(ctx, "defaults", "write", domain, key, typeFlag, val)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
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
