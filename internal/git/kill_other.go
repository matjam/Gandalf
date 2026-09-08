//go:build !windows

package git

import "os/exec"

// kill ends a git command that has passed its deadline. Elsewhere than
// Windows, git is the process that was started, so killing it is enough; an
// ssh it spawned may outlive it, which WaitDelay in execute accounts for.
func kill(cmd *exec.Cmd) error {
	return cmd.Process.Kill()
}
