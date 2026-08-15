//go:build windows

package actions

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"golang.org/x/sys/windows/registry"
)

func readWindowsRegistryValue(ctx context.Context, domain, valueName string) (bool, uint32, []byte, error) {
	if err := ctx.Err(); err != nil {
		return false, 0, nil, err
	}

	hive, path, err := splitWindowsRegistryPath(domain)
	if err != nil {
		return false, 0, nil, err
	}
	key, err := registry.OpenKey(hive, path, registry.QUERY_VALUE)
	if ctxErr := ctx.Err(); ctxErr != nil {
		return false, 0, nil, ctxErr
	}
	if errors.Is(err, registry.ErrNotExist) {
		return false, 0, nil, nil
	}
	if err != nil {
		return false, 0, nil, fmt.Errorf("open registry key: %w", err)
	}
	defer key.Close()

	size, _, err := key.GetValue(valueName, nil)
	if ctxErr := ctx.Err(); ctxErr != nil {
		return false, 0, nil, ctxErr
	}
	if errors.Is(err, registry.ErrNotExist) {
		return false, 0, nil, nil
	}
	if err != nil {
		return false, 0, nil, fmt.Errorf("query registry value size: %w", err)
	}

	data := make([]byte, size)
	for {
		read, actualType, err := key.GetValue(valueName, data)
		if ctxErr := ctx.Err(); ctxErr != nil {
			return false, 0, nil, ctxErr
		}
		if errors.Is(err, registry.ErrNotExist) {
			return false, 0, nil, nil
		}
		if errors.Is(err, registry.ErrShortBuffer) {
			data = make([]byte, read)
			continue
		}
		if err != nil {
			return false, 0, nil, fmt.Errorf("query registry value: %w", err)
		}
		return true, actualType, append([]byte(nil), data[:read]...), nil
	}
}

func splitWindowsRegistryPath(domain string) (registry.Key, string, error) {
	hiveName, path, found := strings.Cut(domain, `\`)
	if !found || path == "" {
		return 0, "", fmt.Errorf("registry path %q must include a hive and subkey", domain)
	}

	var hive registry.Key
	switch strings.ToUpper(hiveName) {
	case "HKCR", "HKEY_CLASSES_ROOT":
		hive = registry.CLASSES_ROOT
	case "HKCU", "HKEY_CURRENT_USER":
		hive = registry.CURRENT_USER
	case "HKLM", "HKEY_LOCAL_MACHINE":
		hive = registry.LOCAL_MACHINE
	case "HKU", "HKEY_USERS":
		hive = registry.USERS
	case "HKCC", "HKEY_CURRENT_CONFIG":
		hive = registry.CURRENT_CONFIG
	default:
		return 0, "", fmt.Errorf("registry path %q has unsupported hive %q", domain, hiveName)
	}
	return hive, path, nil
}
