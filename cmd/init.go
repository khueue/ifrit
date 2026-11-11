package cmd

import (
	"errors"
	"fmt"
	"io/fs"
	"os"

	"github.com/khueue/ifrit/internal/config"
	"github.com/khueue/ifrit/internal/ui"
	"github.com/spf13/cobra"
)

var initCmd = &cobra.Command{
	Use:   "init",
	Short: "Initialize a new ifrit.yml config file",
	Long: `Creates a sample configuration file: ifrit.yml in the current directory,
or the file given by --config.`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		configFile := configPath

		// Never overwrite an existing config.
		if _, err := os.Stat(configFile); err == nil {
			return fmt.Errorf("config file %s already exists", configFile)
		} else if !errors.Is(err, fs.ErrNotExist) {
			return err
		}

		// Create sample config.
		sampleConfig := &config.Config{
			NamePrefix:         "myapp-",
			ImplicitNetworking: new(true),
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
		ui.Printf("1. Edit %s to configure your projects\n", configFile)
		ui.Println("2. Run 'ifrit up' to start all projects")
		ui.Println("")
		ui.Println("Note: implicit_networking is enabled, so your compose.yml files")
		ui.Println("don't need a shared network block.")

		return nil
	},
}

func init() {
	rootCmd.AddCommand(initCmd)
}
