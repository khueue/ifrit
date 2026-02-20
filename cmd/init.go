package cmd

import (
	"fmt"
	"os"

	"github.com/khueue/ifrit/internal/config"
	"github.com/khueue/ifrit/internal/ui"
	"github.com/spf13/cobra"
)

var initCmd = &cobra.Command{
	Use:   "init",
	Short: "Initialize a new ifrit.yml config file",
	Long:  `Creates a sample ifrit.yml configuration file in the current directory.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		configFile := "ifrit.yml"

		// Check if config already exists.
		if _, err := os.Stat(configFile); err == nil {
			return fmt.Errorf("config file %s already exists", configFile)
		}

		// Create sample config.
		sampleConfig := &config.Config{
			NamePrefix:    "myapp",
			SharedNetwork: "myapp_shared",
			Projects: map[string]config.Project{
				"backend": {
					Path:         "./backend",
					ComposeFiles: []string{"compose.yml"},
				},
				"frontend": {
					Path:         "./frontend",
					ComposeFiles: []string{"compose.yml"},
				},
				"database": {
					Path:         "./database",
					ComposeFiles: []string{"compose.yml"},
				},
			},
		}

		if err := sampleConfig.Save(configFile); err != nil {
			return err
		}

		ui.Printf("Created %s\n", configFile)
		ui.Println("\nNext steps:")
		ui.Println("1. Edit ifrit.yml to configure your projects")
		ui.Println("2. Make sure your compose.yml files use the shared network:")
		ui.Println("   networks:")
		ui.Println("     default:")
		ui.Println("       external: true")
		ui.Println("       name: ${IFRIT_SHARED_NETWORK}")
		ui.Println("3. Run 'ifrit up' to start all projects")

		return nil
	},
}

func init() {
	rootCmd.AddCommand(initCmd)
}
