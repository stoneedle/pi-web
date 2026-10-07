package main

import (
	"fmt"
	"os"
	"os/exec"
)

// Windows workers need an executable entry point for the shared Node RPC fixture.
func main() {
	cmd := exec.Command(os.Getenv("PI_WEB_E2E_NODE"), os.Getenv("PI_WEB_E2E_STUB"))
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	if err := cmd.Run(); err != nil {
		if exit, ok := err.(*exec.ExitError); ok {
			os.Exit(exit.ExitCode())
		}
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
