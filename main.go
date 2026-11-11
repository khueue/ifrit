package main

import (
	"errors"
	"os"

	"github.com/khueue/ifrit/cmd"
	"github.com/khueue/ifrit/internal/ui"
)

func main() {
	os.Exit(run())
}

// run executes ifrit and returns its exit code.
func run() int {
	err := cmd.Execute()
	if err == nil {
		return 0
	}
	if silent, ok := errors.AsType[*cmd.SilentExitError](err); ok {
		return silent.Code
	}
	ui.Fprintf(os.Stderr, "Error: %v\n", err)
	return 1
}
