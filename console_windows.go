//go:build windows

package main

import (
	"os"
	"syscall"
)

var (
	procAllocConsole     = kernel32.NewProc("AllocConsole")
	procFreeConsole      = kernel32.NewProc("FreeConsole")
	procGetConsoleWindow = kernel32.NewProc("GetConsoleWindow")
)

// consoleOwned remembers that this process attached a console itself, so the
// setting can tell it apart from one the user started the application from.
var consoleOwned bool

// hasConsole reports whether the process has a console window. Release builds
// use the Windows GUI subsystem and start without one, so writing to stderr
// would fail (and, through io.MultiWriter, silently drop log lines).
func hasConsole() bool {
	if window, _, _ := procGetConsoleWindow.Call(); window != 0 {
		return true
	}
	handle, err := syscall.GetStdHandle(syscall.STD_ERROR_HANDLE)
	return err == nil && handle != 0 && handle != syscall.InvalidHandle
}

// attachConsole gives the windowed application a console window of its own and
// points the standard streams at it, so the log can be followed live instead of
// opening the log file. Only the "console" setting turns it on.
func attachConsole() bool {
	if hasConsole() {
		return true
	}
	if result, _, _ := procAllocConsole.Call(); result == 0 {
		return false
	}
	consoleOwned = true
	rebindStdStreams()
	return true
}

// detachConsole closes a console this process opened itself.
func detachConsole() {
	if !consoleOwned {
		return
	}
	procFreeConsole.Call()
	consoleOwned = false
}

// rebindStdStreams makes os.Stdout, os.Stderr and os.Stdin refer to the newly
// attached console: the handles a GUI process starts with are invalid.
func rebindStdStreams() {
	bind := func(handle syscall.Handle, target **os.File, name string) {
		if handle == 0 || handle == syscall.InvalidHandle {
			return
		}
		*target = os.NewFile(uintptr(handle), name)
	}
	if handle, err := syscall.GetStdHandle(syscall.STD_INPUT_HANDLE); err == nil {
		bind(handle, &os.Stdin, "stdin")
	}
	if handle, err := syscall.GetStdHandle(syscall.STD_OUTPUT_HANDLE); err == nil {
		bind(handle, &os.Stdout, "stdout")
	}
	if handle, err := syscall.GetStdHandle(syscall.STD_ERROR_HANDLE); err == nil {
		bind(handle, &os.Stderr, "stderr")
	}
}
