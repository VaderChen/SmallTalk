//go:build windows

package main

import (
	"os"
	"os/exec"
)

func restartCurrentProcess() error {
	executable, err := os.Executable()
	if err != nil {
		return err
	}
	command := exec.Command(executable, os.Args[1:]...)
	command.Env = os.Environ()
	command.Stdin = os.Stdin
	command.Stdout = os.Stdout
	command.Stderr = os.Stderr
	return command.Start()
}
