package main

import (
	"os"

	"github.com/khueue/ifrit/cmd"
	"github.com/khueue/ifrit/internal/ui"
)

func main() {
	if err := cmd.Execute(); err != nil {
		ui.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}
