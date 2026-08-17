//go:build windows

package procutil

import (
	"errors"
	"os"
	"os/exec"
	"strconv"
)

func configureCommand(cmd *exec.Cmd) {
	cmd.Cancel = func() error {
		if cmd.Process == nil {
			return os.ErrProcessDone
		}
		pid := strconv.Itoa(cmd.Process.Pid)
		if err := exec.Command("taskkill", "/T", "/F", "/PID", pid).Run(); err != nil {
			killErr := cmd.Process.Kill()
			if errors.Is(killErr, os.ErrProcessDone) {
				return os.ErrProcessDone
			}
			return errors.Join(err, killErr)
		}
		return nil
	}
}
