package config

import (
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"

	"gopkg.in/yaml.v3"
)

// Config represents the ifrit.yml configuration file.
type Config struct {
	NamePrefix         string             `yaml:"name_prefix"`
	SharedNetwork      string             `yaml:"shared_network"`
	ImplicitNetworking *bool              `yaml:"implicit_networking"`
	Projects           map[string]Project `yaml:"projects"`
}

// Project represents a Docker Compose subproject.
type Project struct {
	Path         string   `yaml:"path"`
	ComposeFiles []string `yaml:"compose_files,omitempty"`
}

const ConfigFileName = "ifrit.yml"

// Load reads and parses the ifrit.yml configuration file.
func Load(configPath string) (*Config, error) {
	if configPath == "" {
		configPath = ConfigFileName
	}

	wd, err := os.Getwd()
	if err != nil {
		return nil, fmt.Errorf("failed to get working directory: %w", err)
	}

	// Make config path absolute if relative.
	if !filepath.IsAbs(configPath) {
		configPath = filepath.Join(wd, configPath)
	}

	data, err := os.ReadFile(configPath)
	if err != nil {
		return nil, fmt.Errorf("failed to read config file %s: %w", configPath, err)
	}

	var cfg Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("failed to parse config file: %w", err)
	}

	// Allow env vars to override config values.
	if v := os.Getenv("IFRIT_NAME_PREFIX"); v != "" {
		cfg.NamePrefix = v
	}
	if v := os.Getenv("IFRIT_SHARED_NETWORK"); v != "" {
		cfg.SharedNetwork = v
	}

	// Validate required fields.
	if cfg.NamePrefix == "" {
		return nil, fmt.Errorf("name_prefix is required in config")
	}

	if cfg.SharedNetwork == "" {
		return nil, fmt.Errorf("shared_network is required in config")
	}

	if cfg.ImplicitNetworking == nil {
		return nil, fmt.Errorf("implicit_networking is required in config")
	}

	for name, project := range cfg.Projects {
		if len(project.ComposeFiles) == 0 {
			project.ComposeFiles = []string{"compose.yml"}
		}

		if project.Path != "" && !filepath.IsAbs(project.Path) {
			project.Path = filepath.Join(wd, project.Path)
		}

		cfg.Projects[name] = project
	}

	return &cfg, nil
}

// Save writes the configuration to a file.
func (c *Config) Save(configPath string) error {
	if configPath == "" {
		configPath = ConfigFileName
	}

	data, err := yaml.Marshal(c)
	if err != nil {
		return fmt.Errorf("failed to marshal config: %w", err)
	}

	if err := os.WriteFile(configPath, data, 0o644); err != nil {
		return fmt.Errorf("failed to write config file: %w", err)
	}

	return nil
}

// GetProjects returns a sorted list of all project names.
func (c *Config) GetProjects() []string {
	return slices.Sorted(maps.Keys(c.Projects))
}
