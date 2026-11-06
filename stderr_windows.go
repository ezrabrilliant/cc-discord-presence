//go:build windows

package main

import (
	"os"

	"golang.org/x/sys/windows"
)

// redirectStderr makes f the process's standard error handle, which the Go
// runtime writes a panic to; replacing os.Stderr doesn't reach it.
func redirectStderr(f *os.File) error {
	return windows.SetStdHandle(windows.STD_ERROR_HANDLE, windows.Handle(f.Fd()))
}
