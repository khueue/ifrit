package cmd

import (
	"github.com/khueue/ifrit/internal/ui"
	"github.com/spf13/cobra"
)

var statusCmd = &cobra.Command{
	Use:   "status",
	Short: "Show status of all projects",
	Long:  `Display the status of all Docker Compose projects using 'docker compose ps'.`,
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		projects := cfg.GetProjects()
		if len(projects) == 0 {
			ui.Println("No projects defined.")
			return nil
		}

		for _, projectName := range projects {
			ui.Printf("\n=== Project: %s ===\n", projectName)
			if err := printProjectStatus(projectName); err != nil {
				ui.Printf("Error: %v\n", err)
			}
		}

		// Show shared network status.
		ui.Printf("\n=== Network: %s ===\n", cfg.SharedNetwork())
		if err := manager.NetworkStatus(); err != nil {
			ui.Printf("Error checking network: %v\n", err)
		}

		return nil
	},
}

// printProjectStatus lists the project's services, followed by the state of
// its containers.
func printProjectStatus(projectName string) error {
	project, err := manager.Project(projectName)
	if err != nil {
		return err
	}

	services, err := project.Services()
	if err != nil {
		ui.Printf("Error listing services: %v\n", err)
	}
	for _, service := range services {
		ui.Printf("- %s\n", service)
	}

	return project.Status()
}

func init() {
	rootCmd.AddCommand(statusCmd)
}
