//go:build !windows

package logs

import "syscall"

// defaultExecFunc replaces the current process with the given command (Unix only).
var defaultExecFunc = syscall.Exec //nolint:gosec
