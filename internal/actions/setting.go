package actions

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strconv"

	"github.com/atomikpanda/dotular/internal/color"
)

// SettingAction writes a system preference.
// On macOS it calls `defaults write`; on Windows it calls `reg add`.
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
		fmt.Printf("    %s\n", color.Dim(fmt.Sprintf("[dry-run] set: %s %s = %v", a.Domain, a.Key, a.Value)))
		return nil
	}
	switch runtime.GOOS {
	case "darwin":
		return applyMacOSSetting(ctx, a.Domain, a.Key, a.Value)
	case "windows":
		return applyWindowsSetting(ctx, a.Domain, a.Key, a.Value)
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

func applyWindowsSetting(ctx context.Context, regPath, key string, value any) error {
	regType, regVal := windowsValueArgs(value)
	cmd := exec.CommandContext(ctx, "reg", "add", regPath, "/v", key, "/t", regType, "/d", regVal, "/f")
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
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
