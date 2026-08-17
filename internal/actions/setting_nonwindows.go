//go:build !windows

package actions

import (
	"context"
	"errors"
)

func readWindowsRegistryValue(context.Context, string, string) (windowsRegistryValueState, error) {
	return windowsRegistryValueState{}, errors.New("native Windows registry capture is unavailable on this platform")
}
