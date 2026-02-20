package cmd

import (
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
		if len(args) == 0 || downAll {
			if len(cfg.GetProjects()) == 0 {
				ui.Println("No projects defined.")
				return nil
			}
			if err := manager.DownAll(downVolumes); err != nil {
				return err
			}
			return manager.RemoveNetwork()
		}

		// Stop specific projects.
		for _, projectName := range args {
			if err := manager.ComposeDown(projectName, downVolumes); err != nil {
				return err
			}
		}

		return nil
	},
}

func init() {
	downCmd.Flags().BoolVar(&downVolumes, "volumes", false, "Remove volumes")
	downCmd.Flags().BoolVarP(&downAll, "all", "a", false, "Stop all projects")
	rootCmd.AddCommand(downCmd)
}
