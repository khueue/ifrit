package cmd

import (
	"github.com/khueue/ifrit/internal/ui"
	"github.com/spf13/cobra"
)

var (
	upAll   bool
	upFresh bool
)

var upCmd = &cobra.Command{
	Use:   "up [project...]",
	Short: "Start one or more projects",
	Long: `Start one or more Docker Compose projects. If no project names are provided,
starts all projects.

By default, images are rebuilt and orphan containers are removed.
Use --fresh to also force-recreate all containers and their dependencies.`,
	Example: `  # Start all projects
  ifrit up

  # Start specific projects
  ifrit up backend frontend

  # Force-recreate all containers from scratch
  ifrit up --fresh backend`,
	RunE: func(cmd *cobra.Command, args []string) error {
		if len(args) == 0 || upAll {
			if len(cfg.GetProjects()) == 0 {
				ui.Println("No projects defined.")
				return nil
			}
			return manager.UpAll(upFresh)
		}

		// Start specific projects.
		for _, projectName := range args {
			if err := manager.ComposeUp(projectName, upFresh); err != nil {
				return err
			}
		}

		return nil
	},
}

func init() {
	upCmd.Flags().BoolVarP(&upAll, "all", "a", false, "Start all projects")
	upCmd.Flags().BoolVar(&upFresh, "fresh", false, "Force-recreate all containers and their dependencies")
	rootCmd.AddCommand(upCmd)
}
