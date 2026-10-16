//go:build windows

package modelconnector

import (
	"os/exec"
)

// taskkill is a fixed OS tool, never populated from remote command text.
func configureProcess(c *exec.Cmd) {
	c.Cancel = func() error {
		if c.Process == nil {
			return nil
		}
		return exec.Command("taskkill", "/PID", pidString(c.Process.Pid), "/T", "/F").Run()
	}
}
