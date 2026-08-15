//go:build !windows

package actions

import (
	"context"
	"errors"
)

func readWindowsRegistryValue(context.Context, string, string) (bool, uint32, []byte, error) {
	return false, 0, nil, errors.New("native Windows registry capture is unavailable on this platform")
}
