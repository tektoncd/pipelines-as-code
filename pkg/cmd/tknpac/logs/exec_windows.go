//go:build windows

package logs

import (
	"fmt"
	"os"
	osexec "os/exec"
)

// defaultExecFunc runs the command as a subprocess on Windows,
// since syscall.Exec (process replacement) is not available.
var defaultExecFunc = func(argv0 string, argv, envv []string) error {
	cmd := osexec.Command(argv0, argv[1:]...)
	cmd.Env = envv
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("failed to run command: %w", err)
	}
	return nil
}
