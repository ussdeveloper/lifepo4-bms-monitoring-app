//go:build windows

package main

import "syscall"

// hasConsole reports whether the process has a usable standard error handle.
// Release builds use the Windows GUI subsystem and have none, so writing to
// stderr would fail (and, through io.MultiWriter, silently drop log lines).
func hasConsole() bool {
	handle, err := syscall.GetStdHandle(syscall.STD_ERROR_HANDLE)
	if err != nil || handle == 0 || handle == syscall.InvalidHandle {
		return false
	}
	return true
}
