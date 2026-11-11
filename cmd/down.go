package cmd

import (
	"github.com/khueue/ifrit/internal/docker"
	"github.com/khueue/ifrit/internal/ui"
	"github.com/spf13/cobra"
)

var (
	downVolumes bool
	downAll     bool
)

var downCmd = &cobra.Command{
	Use:   "down [project...]",
	Short: "Stop one or more projects",
	Long: `Stop one or more Docker Compose projects. If no project names are provided,
stops all projects.`,
	Example: `  # Stop all projects
  ifrit down

  # Stop specific projects
  ifrit down backend frontend

  # Stop projects and remove volumes
  ifrit down --volumes backend`,
	RunE: func(cmd *cobra.Command, args []string) error {
		all := len(args) == 0 || downAll
		projects := args
		if all {
			projects = cfg.GetProjects()
		}
		if len(projects) == 0 {
			ui.Println("No projects defined.")
			return nil
		}

		if err := manager.Down(projects, docker.DownOptions{RemoveVolumes: downVolumes}); err != nil {
			return err
		}

		// With every project stopped, the shared network has no use.
		if all {
			return manager.RemoveNetwork()
		}
		return nil
	},
}

func init() {
	downCmd.Flags().BoolVar(&downVolumes, "volumes", false, "Remove volumes")
	downCmd.Flags().BoolVarP(&downAll, "all", "a", false, "Stop all projects")
	rootCmd.AddCommand(downCmd)
}
