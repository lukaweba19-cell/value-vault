//go:build !windows

package r2store

import "os/exec"

func hiddenCmd(name string, args ...string) *exec.Cmd {
	return exec.Command(name, args...)
}
