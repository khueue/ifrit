package cmd

import (
	"fmt"

	"github.com/khueue/ifrit/internal/config"
	"github.com/khueue/ifrit/internal/docker"
	"github.com/khueue/ifrit/internal/ui"
	"github.com/spf13/cobra"
)

const version = "0.1.0"

var (
	configPath string
	verbose    bool
	cfg        *config.Config
	manager    *docker.Manager
)

var rootCmd = &cobra.Command{
	Use:   "ifrit",
	Short: "Ifrit - Multi-project Docker Compose orchestrator",
	Long: `Ifrit is a CLI tool that wraps Docker Compose to manage multiple subprojects
with their own compose files, allowing them to be started/stopped on demand
while sharing a common network.`,
	PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
		// Skip loading config for commands that don't need it.
		for c := cmd; c != nil; c = c.Parent() {
			switch c.Name() {
			case "init", "version", "completion":
				return nil
			}
		}

		var err error
		cfg, err = config.Load(configPath)
		if err != nil {
			return fmt.Errorf("failed to load config: %w", err)
		}

		manager = docker.NewManager(cfg, verbose)
		return nil
	},
}

// Execute runs the root command.
func Execute() error {
	rootCmd.SilenceErrors = true
	rootCmd.SilenceUsage = true
	return rootCmd.Execute()
}

var versionCmd = &cobra.Command{
	Use:   "version",
	Short: "Print the version of Ifrit",
	Run: func(cmd *cobra.Command, args []string) {
		ui.Printf("ifrit %s\n", version)
	},
}

func init() {
	rootCmd.PersistentFlags().StringVarP(&configPath, "config", "c", "ifrit.yml", "path to config file")
	rootCmd.PersistentFlags().BoolVarP(&verbose, "verbose", "v", false, "print all underlying commands being executed")
	rootCmd.AddCommand(versionCmd)
}
