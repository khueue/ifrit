package cmd

import (
	"fmt"
	"slices"
	"strings"

	"github.com/khueue/ifrit/internal/ui"
	"github.com/spf13/cobra"
)

var shellInteractive bool

func printShellUsageHint() {
	projects := cfg.GetProjects()
	if len(projects) == 0 {
		ui.Println("No projects defined.")
		return
	}

	ui.Println("Available services:")

	for _, projectName := range projects {
		services, err := manager.ComposeServices(projectName)
		if err != nil {
			ui.Printf("Error listing services for %s: %v\n", projectName, err)
			continue
		}

		ui.Printf("\n%s:\n", projectName)
		if len(services) == 0 {
			ui.Println("  (no services defined)")
		} else {
			for _, service := range services {
				ui.Printf("  ifrit shell %s %s\n", projectName, service)
			}
		}
	}
	ui.Println()
	ui.Println("Run 'ifrit shell --help' for full usage information.")
}

var shellCmd = &cobra.Command{
	Use:   "shell <project> <service> [-- command [args...]]",
	Short: "Open a shell or execute a command in a running container",
	Long: `Open an interactive shell or execute a command in a running container.

If no command is specified, opens an interactive shell (/bin/bash or /bin/sh).
If a command is provided after '--', executes that command in the container.

The project must be running for this command to work.`,
	Example: `  # Open an interactive shell in the backend api service
  ifrit shell backend api

  # Execute a command in the container
  ifrit shell backend api -- ls -al

  # Run command non-interactively (for scripts)
  ifrit shell --interactive=false backend api -- env > output.txt`,
	RunE: func(cmd *cobra.Command, args []string) error {
		// Because SetInterspersed(false) is used, cobra/pflag does not
		// process "--" itself, so we parse it manually from the args slice.
		dashIndex := slices.Index(args, "--")

		var positionalArgs []string
		var commandArgs []string

		if dashIndex >= 0 {
			positionalArgs = args[:dashIndex]
			commandArgs = args[dashIndex+1:]
		} else {
			positionalArgs = args
		}

		if len(positionalArgs) < 2 {
			printShellUsageHint()
			if len(positionalArgs) == 0 {
				return fmt.Errorf("requires a project and service name")
			}
			return fmt.Errorf("requires a service name (project: %s)", positionalArgs[0])
		}

		// If extra positional args were given without "--", nudge the user.
		if len(positionalArgs) > 2 {
			return fmt.Errorf(
				"use '--' to separate container commands from ifrit arguments:\n  ifrit shell %s %s -- %s",
				positionalArgs[0], positionalArgs[1], strings.Join(positionalArgs[2:], " "),
			)
		}

		projectName := positionalArgs[0]
		serviceName := positionalArgs[1]

		// Validate project exists.
		if !slices.Contains(cfg.GetProjects(), projectName) {
			printShellUsageHint()
			return fmt.Errorf("project %q not found", projectName)
		}

		// Validate service exists in the project.
		services, err := manager.ComposeServices(projectName)
		if err != nil {
			printShellUsageHint()
			return fmt.Errorf("failed to list services for %s: %w", projectName, err)
		}
		if !slices.Contains(services, serviceName) {
			ui.Printf("Service %q not found in project %q.\n\n", serviceName, projectName)
			ui.Printf("Available services in %s:\n", projectName)
			for _, s := range services {
				ui.Printf("  ifrit shell %s %s\n", projectName, s)
			}
			ui.Println()
			ui.Println("Run 'ifrit shell --help' for full usage information.")
			return fmt.Errorf("service %q not found in project %q", serviceName, projectName)
		}

		var command []string

		if dashIndex == -1 || len(commandArgs) == 0 {
			// No "--" provided, or nothing after it; open an interactive shell.
			command = []string{"/bin/bash"}
		} else {
			command = commandArgs
		}

		return manager.ComposeExec(projectName, serviceName, command, shellInteractive)
	},
}

func init() {
	shellCmd.Flags().BoolVarP(&shellInteractive, "interactive", "i", true, "Run in interactive mode with TTY")
	shellCmd.Flags().SetInterspersed(false)
	rootCmd.AddCommand(shellCmd)
}
