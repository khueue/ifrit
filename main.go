package main

import (
	"errors"
	"os"

	"github.com/khueue/ifrit/cmd"
	"github.com/khueue/ifrit/internal/ui"
)

func main() {
	if err := cmd.Execute(); err != nil {
		if silent, ok := errors.AsType[*cmd.SilentExitError](err); ok {
			os.Exit(silent.Code)
		}
		ui.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}
