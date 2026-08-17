//go:build windows

package actions

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"golang.org/x/sys/windows/registry"
)

func readWindowsRegistryValue(ctx context.Context, domain, valueName string) (windowsRegistryValueState, error) {
	if err := ctx.Err(); err != nil {
		return windowsRegistryValueState{}, err
	}

	hive, path, err := splitWindowsRegistryPath(domain)
	if err != nil {
		return windowsRegistryValueState{}, err
	}
	hiveName, _, _ := strings.Cut(domain, `\`)
	parts := strings.Split(path, `\`)
	var targetKey registry.Key
	for index, part := range parts {
		if part == "" {
			return windowsRegistryValueState{}, fmt.Errorf("registry path %q contains an empty subkey", domain)
		}
		prefix := strings.Join(parts[:index+1], `\`)
		key, openErr := registry.OpenKey(hive, prefix, registry.QUERY_VALUE)
		if ctxErr := ctx.Err(); ctxErr != nil {
			if openErr == nil {
				_ = key.Close()
			}
			return windowsRegistryValueState{}, ctxErr
		}
		if errors.Is(openErr, registry.ErrNotExist) {
			return windowsRegistryValueState{deleteKey: hiveName + `\` + prefix}, nil
		}
		if openErr != nil {
			return windowsRegistryValueState{}, fmt.Errorf("open registry key: %w", openErr)
		}
		if index == len(parts)-1 {
			targetKey = key
		} else {
			_ = key.Close()
		}
	}
	defer targetKey.Close()

	size, _, err := targetKey.GetValue(valueName, nil)
	if ctxErr := ctx.Err(); ctxErr != nil {
		return windowsRegistryValueState{}, ctxErr
	}
	if errors.Is(err, registry.ErrNotExist) {
		return windowsRegistryValueState{}, nil
	}
	if err != nil {
		return windowsRegistryValueState{}, fmt.Errorf("query registry value size: %w", err)
	}

	data := make([]byte, size)
	for {
		read, actualType, err := targetKey.GetValue(valueName, data)
		if ctxErr := ctx.Err(); ctxErr != nil {
			return windowsRegistryValueState{}, ctxErr
		}
		if errors.Is(err, registry.ErrNotExist) {
			return windowsRegistryValueState{}, nil
		}
		if errors.Is(err, registry.ErrShortBuffer) {
			data = make([]byte, read)
			continue
		}
		if err != nil {
			return windowsRegistryValueState{}, fmt.Errorf("query registry value: %w", err)
		}
		return windowsRegistryValueState{
			present:   true,
			valueType: actualType,
			data:      append([]byte(nil), data[:read]...),
		}, nil
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
