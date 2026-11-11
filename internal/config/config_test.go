package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/khueue/ifrit/internal/check"
)

// writeConfig writes content to ifrit.yml in a fresh temp dir and returns the
// directory (with symlinks resolved, so paths compare equal on macOS).
func writeConfig(t *testing.T, content string) string {
	t.Helper()
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ConfigFileName), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

// mustLoad loads the config at path, stopping the test if that fails.
func mustLoad(t *testing.T, path string) *Config {
	t.Helper()
	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	return cfg
}

const validConfig = `
name_prefix: myapp-
implicit_networking: true
projects:
  frontend:
    path: ./frontend
  backend:
    path: backend
    compose_files:
      - compose.yml
      - compose.dev.yml
`

func TestLoad(t *testing.T) {
	dir := writeConfig(t, validConfig)
	t.Chdir(dir)

	cfg := mustLoad(t, "")

	check.Equal(t, "NamePrefix", cfg.NamePrefix, "myapp-")
	check.Equal(t, "ImplicitNetworking", *cfg.ImplicitNetworking, true)
	check.Equal(t, "SharedNetwork()", cfg.SharedNetwork(), "myapp-shared")
	check.Slice(t, "GetProjects() (sorted)", cfg.GetProjects(), []string{"backend", "frontend"})

	backend := cfg.Projects["backend"]
	check.Equal(t, "backend path", backend.Path, filepath.Join(dir, "backend"))
	check.Slice(t, "backend compose files", backend.ComposeFiles, []string{"compose.yml", "compose.dev.yml"})

	// compose_files defaults to compose.yml.
	check.Slice(t, "frontend compose files", cfg.Projects["frontend"].ComposeFiles, []string{"compose.yml"})
}

// Project paths belong to the config file, not to wherever ifrit happens to
// be run from: "ifrit -c ../ifrit.yml up" must find the same projects as
// running "ifrit up" next to the file.
func TestLoadResolvesPathsAgainstConfigDir(t *testing.T) {
	dir := writeConfig(t, validConfig)
	t.Chdir(t.TempDir()) // somewhere else entirely

	cfg, err := Load(filepath.Join(dir, ConfigFileName))
	if err != nil {
		t.Fatal(err)
	}
	if got, want := cfg.Projects["frontend"].Path, filepath.Join(dir, "frontend"); got != want {
		t.Errorf("frontend path = %q, want %q", got, want)
	}
}

func TestLoadRelativeConfigPath(t *testing.T) {
	dir := writeConfig(t, validConfig)
	sub := filepath.Join(dir, "sub")
	if err := os.Mkdir(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Chdir(sub)

	cfg, err := Load(filepath.Join("..", ConfigFileName))
	if err != nil {
		t.Fatal(err)
	}
	if got, want := cfg.Projects["backend"].Path, filepath.Join(dir, "backend"); got != want {
		t.Errorf("backend path = %q, want %q", got, want)
	}
}

func TestLoadKeepsAbsolutePaths(t *testing.T) {
	abs := t.TempDir()
	dir := writeConfig(t, "name_prefix: x-\nimplicit_networking: false\nprojects:\n  a:\n    path: "+abs+"\n")

	cfg, err := Load(filepath.Join(dir, ConfigFileName))
	if err != nil {
		t.Fatal(err)
	}
	if got := cfg.Projects["a"].Path; got != abs {
		t.Errorf("path = %q, want %q", got, abs)
	}
	if *cfg.ImplicitNetworking {
		t.Error("ImplicitNetworking = true, want false")
	}
}

func TestLoadEnvOverrides(t *testing.T) {
	dir := writeConfig(t, validConfig)
	path := filepath.Join(dir, ConfigFileName)

	t.Run("name prefix", func(t *testing.T) {
		t.Setenv("IFRIT_NAME_PREFIX", "other-")
		cfg, err := Load(path)
		if err != nil {
			t.Fatal(err)
		}
		if cfg.NamePrefix != "other-" {
			t.Errorf("NamePrefix = %q", cfg.NamePrefix)
		}
		if got, want := cfg.SharedNetwork(), "other-shared"; got != want {
			t.Errorf("SharedNetwork() = %q, want %q", got, want)
		}
	})

	t.Run("shared network", func(t *testing.T) {
		t.Setenv("IFRIT_SHARED_NETWORK", "custom-net")
		cfg, err := Load(path)
		if err != nil {
			t.Fatal(err)
		}
		if got, want := cfg.SharedNetwork(), "custom-net"; got != want {
			t.Errorf("SharedNetwork() = %q, want %q", got, want)
		}
		if cfg.NamePrefix != "myapp-" {
			t.Errorf("NamePrefix = %q, want it untouched", cfg.NamePrefix)
		}
	})

	t.Run("name prefix from env satisfies the required field", func(t *testing.T) {
		dir := writeConfig(t, "implicit_networking: true\n")
		t.Setenv("IFRIT_NAME_PREFIX", "env-")
		if _, err := Load(filepath.Join(dir, ConfigFileName)); err != nil {
			t.Errorf("unexpected error: %v", err)
		}
	})
}

func TestLoadErrors(t *testing.T) {
	tests := []struct {
		name    string
		content string
		wantErr string
	}{
		{"missing name_prefix", "implicit_networking: true\n", "name_prefix is required"},
		{"missing implicit_networking", "name_prefix: x-\n", "implicit_networking is required"},
		{"unknown top-level field", "name_prefix: x-\nimplicit_networking: true\nnetwork: foo\n", "network"},
		{"unknown project field", "name_prefix: x-\nimplicit_networking: true\nprojects:\n  a:\n    path: .\n    compose_file: x.yml\n", "compose_file"},
		{"project without path", "name_prefix: x-\nimplicit_networking: true\nprojects:\n  b:\n    path: ./b\n  a:\n    compose_files: [x.yml]\n", "path is required for project a"},
		{"null project", "name_prefix: x-\nimplicit_networking: true\nprojects:\n  a:\n", "path is required for project a"},
		{"invalid yaml", "name_prefix: [\n", "failed to parse config file"},
		{"wrong type", "name_prefix: x-\nimplicit_networking: maybe\n", "failed to parse config file"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := writeConfig(t, tt.content)
			_, err := Load(filepath.Join(dir, ConfigFileName))
			if err == nil {
				t.Fatal("expected an error")
			}
			if !strings.Contains(err.Error(), tt.wantErr) {
				t.Errorf("error = %q, want it to contain %q", err, tt.wantErr)
			}
		})
	}
}

func TestLoadMissingFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nope.yml")
	_, err := Load(path)
	if err == nil {
		t.Fatal("expected an error")
	}
	if n := strings.Count(err.Error(), "nope.yml"); n != 1 {
		t.Errorf("error should name the file exactly once, got %d times: %v", n, err)
	}
}

func TestLoadNoProjects(t *testing.T) {
	dir := writeConfig(t, "name_prefix: x-\nimplicit_networking: true\n")
	cfg, err := Load(filepath.Join(dir, ConfigFileName))
	if err != nil {
		t.Fatal(err)
	}
	if got := cfg.GetProjects(); len(got) != 0 {
		t.Errorf("GetProjects() = %v, want none", got)
	}
}

// What Save writes, Load must read back.
func TestSaveLoadRoundTrip(t *testing.T) {
	dir, err := filepath.EvalSymlinks(t.TempDir())
	check.NoError(t, err)
	path := filepath.Join(dir, "custom.yml")

	in := &Config{
		NamePrefix:            "rt-",
		sharedNetworkOverride: "runtime-only",
		ImplicitNetworking:    new(false),
		Projects: map[string]Project{
			"api": {Path: "./api", ComposeFiles: []string{"a.yml", "b.yml"}},
			"web": {Path: "./web"},
		},
	}
	check.NoError(t, in.Save(path))

	out := mustLoad(t, path)
	check.Equal(t, "NamePrefix", out.NamePrefix, "rt-")
	check.Equal(t, "ImplicitNetworking", *out.ImplicitNetworking, false)
	check.Equal(t, "api path", out.Projects["api"].Path, filepath.Join(dir, "api"))
	check.Slice(t, "api compose files", out.Projects["api"].ComposeFiles, []string{"a.yml", "b.yml"})

	// The shared-network override is runtime state and must not be persisted.
	check.Equal(t, "SharedNetwork()", out.SharedNetwork(), "rt-shared")
}
