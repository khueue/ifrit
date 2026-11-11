package cmd

import (
	"github.com/khueue/ifrit/internal/docker"
	"github.com/khueue/ifrit/internal/ui"
	"github.com/spf13/cobra"
)

var (
	upAll      bool
	upRecreate bool
)

var upCmd = &cobra.Command{
	Use:   "up [project...]",
	Short: "Start one or more projects",
	Long: `Start one or more Docker Compose projects. If no project names are provided,
starts all projects.

By default, missing images are built and orphan containers are removed.
Use --recreate to also rebuild all images and force-recreate all containers
and their dependencies.`,
	Example: `  # Start all projects
  ifrit up

  # Start specific projects
  ifrit up backend frontend

  # Force-recreate all containers from scratch
  ifrit up --recreate backend`,
	RunE: func(cmd *cobra.Command, args []string) error {
		projects := args
		if len(args) == 0 || upAll {
			projects = cfg.GetProjects()
		}
		if len(projects) == 0 {
			ui.Println("No projects defined.")
			return nil
		}

		return manager.Up(projects, docker.UpOptions{Recreate: upRecreate})
	},
}

func init() {
	upCmd.Flags().BoolVarP(&upAll, "all", "a", false, "Start all projects")
	upCmd.Flags().BoolVar(&upRecreate, "recreate", false, "Rebuild images and force-recreate all containers and their dependencies")
	rootCmd.AddCommand(upCmd)
}
