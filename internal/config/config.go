// Package config loads and saves the ifrit.yml configuration file.
package config

import (
	"errors"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"

	"go.yaml.in/yaml/v4"
)

// Config represents the ifrit.yml configuration file.
type Config struct {
	NamePrefix            string             `yaml:"name_prefix"`
	sharedNetworkOverride string             `yaml:"-"`
	ImplicitNetworking    *bool              `yaml:"implicit_networking"`
	Projects              map[string]Project `yaml:"projects"`
}

// SharedNetwork returns the shared Docker network name.
// If IFRIT_SHARED_NETWORK was set, that value is used; otherwise the name is
// derived as {name_prefix}shared.
func (c *Config) SharedNetwork() string {
	if c.sharedNetworkOverride != "" {
		return c.sharedNetworkOverride
	}
	return c.NamePrefix + "shared"
}

// Project represents a Docker Compose subproject.
type Project struct {
	Path         string   `yaml:"path"`
	ComposeFiles []string `yaml:"compose_files,omitempty"`
}

// ConfigFileName is the default name of the configuration file.
const ConfigFileName = "ifrit.yml"

// Load reads, validates and normalizes the configuration file at configPath.
// Relative project paths are resolved against the directory of the config
// file, so the result does not depend on where ifrit is run from.
func Load(configPath string) (*Config, error) {
	if configPath == "" {
		configPath = ConfigFileName
	}

	configPath, err := filepath.Abs(configPath)
	if err != nil {
		return nil, fmt.Errorf("failed to resolve config path: %w", err)
	}

	data, err := os.ReadFile(configPath)
	if err != nil {
		return nil, fmt.Errorf("failed to read config file: %w", err)
	}

	var cfg Config
	if err := yaml.Load(data, &cfg, yaml.WithKnownFields()); err != nil {
		return nil, fmt.Errorf("failed to parse config file %s: %w", configPath, err)
	}

	cfg.applyEnv()
	if err := cfg.validate(); err != nil {
		return nil, err
	}
	cfg.normalizeProjects(filepath.Dir(configPath))

	return &cfg, nil
}

// applyEnv lets environment variables override config values.
func (c *Config) applyEnv() {
	if v := os.Getenv("IFRIT_NAME_PREFIX"); v != "" {
		c.NamePrefix = v
	}
	if v := os.Getenv("IFRIT_SHARED_NETWORK"); v != "" {
		c.sharedNetworkOverride = v
	}
}

// validate checks that all required fields are set.
func (c *Config) validate() error {
	if c.NamePrefix == "" {
		return errors.New("name_prefix is required in config")
	}
	if c.ImplicitNetworking == nil {
		return errors.New("implicit_networking is required in config")
	}
	for _, name := range c.GetProjects() {
		if c.Projects[name].Path == "" {
			return fmt.Errorf("path is required for project %s in config", name)
		}
	}
	return nil
}

// normalizeProjects makes project paths absolute, resolving relative ones
// against configDir, and fills in the default compose file.
func (c *Config) normalizeProjects(configDir string) {
	for name, project := range c.Projects {
		if !filepath.IsAbs(project.Path) {
			project.Path = filepath.Join(configDir, project.Path)
		}
		if len(project.ComposeFiles) == 0 {
			project.ComposeFiles = []string{"compose.yml"}
		}
		c.Projects[name] = project
	}
}

// Save writes the configuration to a file.
func (c *Config) Save(configPath string) error {
	if configPath == "" {
		configPath = ConfigFileName
	}

	data, err := yaml.Dump(c, yaml.WithV4Defaults())
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
