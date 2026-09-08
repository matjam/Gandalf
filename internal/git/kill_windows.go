//go:build windows

package git

import (
	"os/exec"
	"strconv"
)

// kill ends a git command that has passed its deadline, and everything it
// started.
//
// On Windows the git on PATH is usually Git for Windows' launcher, a small
// executable that runs the real git as a child and waits for it. Killing the
// launcher alone leaves that child running with the vault as its working
// directory, still holding the connection the deadline was meant to cut, and
// still holding the directory open — which is how a stalled fetch kept a
// temporary vault from being removed. taskkill /T takes the tree.
func kill(cmd *exec.Cmd) error {
	pid := strconv.Itoa(cmd.Process.Pid)
	if err := exec.Command("taskkill", "/T", "/F", "/PID", pid).Run(); err != nil {
		// taskkill can fail if the tree is already gone; the plain kill then
		// reports the real state.
		return cmd.Process.Kill()
	}
	return nil
}
