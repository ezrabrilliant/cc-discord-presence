//go:build !windows

package main

import (
	"os"

	"golang.org/x/sys/unix"
)

// redirectStderr makes f file descriptor 2, which the Go runtime writes a
// panic to; replacing os.Stderr doesn't reach it.
func redirectStderr(f *os.File) error {
	return unix.Dup2(int(f.Fd()), 2)
}
