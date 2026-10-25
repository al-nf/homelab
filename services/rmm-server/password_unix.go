//go:build linux || darwin

package main

import (
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"unsafe"
)

func terminalAttributes(file *os.File, operation uintptr, state *syscall.Termios) error {
	_, _, err := syscall.Syscall(syscall.SYS_IOCTL, file.Fd(), operation, uintptr(unsafe.Pointer(state)))
	if err != 0 {
		return err
	}
	return nil
}

func setPasswordInteractive(input *os.File, path string) error {
	var original syscall.Termios
	if err := terminalAttributes(input, terminalGet, &original); err != nil {
		// Piped input remains supported and requires no terminal interaction.
		return setPasswordFrom(input, path)
	}
	hidden := original
	hidden.Lflag &^= syscall.ECHO | syscall.ECHONL
	if err := terminalAttributes(input, terminalSet, &hidden); err != nil {
		return err
	}
	restore := func() { _ = terminalAttributes(input, terminalSet, &original) }
	defer restore()
	// Restore terminal echo even if the user cancels at the prompt.
	interrupted := make(chan os.Signal, 1)
	signal.Notify(interrupted, os.Interrupt, syscall.SIGTERM)
	defer signal.Stop(interrupted)
	done := make(chan struct{})
	defer close(done)
	go func() {
		select {
		case <-interrupted:
			restore()
			fmt.Fprintln(os.Stderr)
			os.Exit(130)
		case <-done:
		}
	}()
	fmt.Fprint(os.Stderr, "Dashboard password (at least 16 characters): ")
	err := setPasswordFrom(input, path)
	fmt.Fprintln(os.Stderr)
	return err
}
