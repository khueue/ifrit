package cmd

import (
	"os/exec"

	"github.com/khueue/ifrit/internal/ui"
	"github.com/khueue/ifrit/internal/ui/logsviewer"
	"github.com/spf13/cobra"
)

var (
	logsFollow bool
	logsTail   string
	logsNoTUI  bool
)

var logsCmd = &cobra.Command{
	Use:   "logs [project...]",
	Short: "View logs for one or more projects",
	Long: `Display logs from Docker Compose projects.

By default, launches an interactive TUI with one tab per project, tailing logs
in real time. Use --no-tui to fall back to plain output.`,
	Example: `  # Interactive TUI with all projects (default)
  ifrit logs

  # Interactive TUI with specific projects
  ifrit logs backend frontend

  # Plain output (no TUI)
  ifrit logs --no-tui backend

  # Plain output, follow logs
  ifrit logs --no-tui -f backend

  # Plain output, show last 100 lines
  ifrit logs --no-tui --tail 100 backend`,
	RunE: func(cmd *cobra.Command, args []string) error {
		projects := args
		if len(projects) == 0 {
			projects = cfg.GetProjects()
		}
		if len(projects) == 0 {
			ui.Println("No projects defined.")
			return nil
		}

		if logsNoTUI {
			return runPlainLogs(projects)
		}
		return runInteractiveLogs(projects)
	},
}

func runInteractiveLogs(projects []string) error {
	tail := logsTail
	if tail == "all" {
		// For the TUI, default to a reasonable number of lines so startup
		// is fast. Users can override with --tail.
		tail = "100"
	}

	return logsviewer.Run(projects, func(projectName string) (*exec.Cmd, error) {
		return manager.ComposeLogsCmd(projectName, tail)
	})
}

func runPlainLogs(projects []string) error {
	for i, projectName := range projects {
		if len(projects) > 1 {
			if i > 0 {
				ui.Println()
			}
			ui.Printf("=== Logs: %s ===\n", projectName)
		}
		if err := manager.ComposeLogs(projectName, logsFollow, logsTail); err != nil {
			if len(projects) > 1 {
				ui.Printf("Error: %v\n", err)
				continue
			}
			return err
		}
	}
	return nil
}

func init() {
	logsCmd.Flags().BoolVarP(&logsFollow, "follow", "f", false, "Follow log output (only with --no-tui)")
	logsCmd.Flags().StringVar(&logsTail, "tail", "all", "Number of lines to show from the end of the logs")
	logsCmd.Flags().BoolVar(&logsNoTUI, "no-tui", false, "Disable interactive TUI, print logs to stdout")
	rootCmd.AddCommand(logsCmd)
}
