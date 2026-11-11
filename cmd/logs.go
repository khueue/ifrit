package cmd

import (
	"errors"
	"os"
	"os/exec"

	"github.com/khueue/ifrit/internal/docker"
	"github.com/khueue/ifrit/internal/ui"
	"github.com/khueue/ifrit/internal/ui/logsviewer"
	"github.com/mattn/go-isatty"
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

By default, launches an interactive TUI with one tab per service across all
projects, tailing logs in real time. Use --no-tui to fall back to plain output;
this also happens by itself when the output is piped or redirected.`,
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

		// The TUI needs a terminal to draw on.
		if logsNoTUI || !isatty.IsTerminal(os.Stdout.Fd()) {
			return runPlainLogs(projects)
		}
		return runInteractiveLogs(projects)
	},
}

func runInteractiveLogs(projects []string) error {
	tail := logsTail
	if tail == "" {
		// For the TUI, default to a reasonable number of lines so startup
		// is fast. Users can override with --tail.
		tail = "100"
	}

	// Expand each project into one tab per service, remembering the command
	// that tails each tab's logs.
	var tabs []logsviewer.TabInfo
	cmds := map[string]*exec.Cmd{}
	for _, projectName := range projects {
		project, err := manager.Project(projectName)
		if err != nil {
			return err
		}
		services, err := project.Services()
		if err != nil {
			return err
		}
		for _, service := range services {
			label := projectName + "/" + service
			tabs = append(tabs, logsviewer.TabInfo{Name: label, Group: projectName})
			cmds[label] = project.LogsCmd(docker.LogsOptions{Follow: true, Tail: tail, Service: service})
		}
	}

	if len(tabs) == 0 {
		ui.Println("No services found.")
		return nil
	}

	return logsviewer.Run(tabs, func(tabName string) (*exec.Cmd, error) {
		return cmds[tabName], nil
	})
}

func runPlainLogs(projects []string) error {
	if len(projects) == 1 {
		return printLogs(projects[0])
	}

	// Following a project only ends when the project stops, so the projects
	// after the first one would never be reached.
	if logsFollow {
		return errors.New("--follow without the TUI can only follow one project at a time; name a single project")
	}

	for i, projectName := range projects {
		if i > 0 {
			ui.Println()
		}
		ui.Printf("=== Logs: %s ===\n", projectName)
		if err := printLogs(projectName); err != nil {
			ui.Printf("Error: %v\n", err)
		}
	}
	return nil
}

// printLogs prints the logs of a single project.
func printLogs(projectName string) error {
	project, err := manager.Project(projectName)
	if err != nil {
		return err
	}
	return project.Logs(docker.LogsOptions{Follow: logsFollow, Tail: logsTail})
}

func init() {
	logsCmd.Flags().BoolVarP(&logsFollow, "follow", "f", false, "Follow log output (only with --no-tui)")
	logsCmd.Flags().StringVar(&logsTail, "tail", "", "Number of lines to show from the end of the logs, or \"all\" (default: 100 in the TUI, all without it)")
	logsCmd.Flags().BoolVar(&logsNoTUI, "no-tui", false, "Disable interactive TUI, print logs to stdout")
	rootCmd.AddCommand(logsCmd)
}
