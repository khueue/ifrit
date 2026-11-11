// Package docker runs Docker and Docker Compose commands for ifrit projects.
package docker

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"

	"github.com/khueue/ifrit/internal/config"
	"github.com/khueue/ifrit/internal/ui"
)

// outputError adds the stderr captured by cmd.Output to err: the exit status
// alone says nothing about what went wrong.
func outputError(err error) error {
	if exitErr, ok := errors.AsType[*exec.ExitError](err); ok {
		if stderr := strings.TrimSpace(string(exitErr.Stderr)); stderr != "" {
			return fmt.Errorf("%w: %s", err, stderr)
		}
	}
	return err
}

// ExecExitError is returned by Project.Exec when the command it ran inside the
// container exited unsuccessfully. It is kept apart from failures to get as
// far as running the command (no daemon, service fails to start, ...), which
// callers will want to report rather than pass on as an exit status.
type ExecExitError struct {
	Code int
}

func (e *ExecExitError) Error() string {
	return fmt.Sprintf("exit status %d", e.Code)
}

// Manager handles Docker Compose operations.
type Manager struct {
	config          *config.Config
	verbose         bool
	networkVerified bool
	overrideFile    string // generated compose override for implicit networking
}

// NewManager creates a new Docker manager.
func NewManager(cfg *config.Config, verbose bool) *Manager {
	return &Manager{
		config:  cfg,
		verbose: verbose,
	}
}

// logCommand prints the full command line when verbose mode is enabled.
func (m *Manager) logCommand(cmd *exec.Cmd) {
	if !m.verbose {
		return
	}
	ui.Debugf("%s\n", strings.Join(cmd.Args, " "))
}

// run runs cmd with its output going to the terminal.
func (m *Manager) run(cmd *exec.Cmd) error {
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	m.logCommand(cmd)
	return cmd.Run()
}

// composeEnv returns the current process environment with IFRIT_SHARED_NETWORK injected.
// When not in verbose mode, BUILDKIT_PROGRESS is set to "quiet" to suppress
// noisy BuildKit output during image builds.
func (m *Manager) composeEnv() []string {
	env := append(os.Environ(), fmt.Sprintf("IFRIT_SHARED_NETWORK=%s", m.config.SharedNetwork()))
	if !m.verbose {
		env = append(env, "BUILDKIT_PROGRESS=quiet")
	}
	return env
}

// ensureOverrideFile creates (once) a compose override file that sets the
// default network to the shared external network.
//
// The file lives in the user's cache directory rather than the system temp
// directory. Its path is predictable, so in a directory other users can write
// to, one of them could plant a file there and have it merged into our
// compose projects.
func (m *Manager) ensureOverrideFile() (string, error) {
	if m.overrideFile != "" {
		return m.overrideFile, nil
	}

	cacheDir, err := os.UserCacheDir()
	if err != nil {
		return "", fmt.Errorf("failed to locate a directory for the network override file: %w", err)
	}
	dir := filepath.Join(cacheDir, "ifrit")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", fmt.Errorf("failed to create directory for the network override file: %w", err)
	}

	content := fmt.Sprintf(`networks:
  default:
    external: true
    name: %q
`, m.config.SharedNetwork())

	// Use a deterministic path so we reuse the same file across runs.
	path := filepath.Join(dir, fmt.Sprintf("network-override-%s.yml", m.config.SharedNetwork()))

	// Skip writing if the file already has the correct content.
	existing, err := os.ReadFile(path)
	if err != nil || string(existing) != content {
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			return "", fmt.Errorf("failed to write network override file: %w", err)
		}
	}

	m.overrideFile = path
	return m.overrideFile, nil
}

// networkExists reports whether the shared network exists.
func (m *Manager) networkExists() (bool, error) {
	cmd := exec.Command("docker", "network", "ls", "--format", "{{.Name}}")
	m.logCommand(cmd)
	output, err := cmd.Output()
	if err != nil {
		return false, fmt.Errorf("failed to list docker networks: %w", outputError(err))
	}

	return slices.Contains(strings.Fields(string(output)), m.config.SharedNetwork()), nil
}

// EnsureNetwork creates the shared network if it doesn't exist.
func (m *Manager) EnsureNetwork() error {
	if m.networkVerified {
		return nil
	}

	exists, err := m.networkExists()
	if err != nil {
		return err
	}
	if exists {
		if m.verbose {
			ui.Printf("Network %s already exists\n", m.config.SharedNetwork())
		}
		m.networkVerified = true
		return nil
	}

	// Create network.
	ui.Printf("Creating shared network: %s\n", m.config.SharedNetwork())
	cmd := exec.Command("docker", "network", "create", m.config.SharedNetwork())
	if err := m.run(cmd); err != nil {
		return fmt.Errorf("failed to create network %s: %w", m.config.SharedNetwork(), err)
	}

	m.networkVerified = true
	return nil
}

// NetworkStatus runs docker network ls filtered by the shared network name,
// printing the result directly to stdout.
func (m *Manager) NetworkStatus() error {
	cmd := exec.Command("docker", "network", "ls", "--filter", fmt.Sprintf("name=^%s$", m.config.SharedNetwork()))
	if err := m.run(cmd); err != nil {
		return fmt.Errorf("failed to check network %s: %w", m.config.SharedNetwork(), err)
	}

	return nil
}

// RemoveNetwork removes the shared network if it exists.
func (m *Manager) RemoveNetwork() error {
	exists, err := m.networkExists()
	if err != nil {
		return err
	}
	if !exists {
		return nil
	}

	ui.Printf("Removing shared network: %s\n", m.config.SharedNetwork())
	cmd := exec.Command("docker", "network", "rm", m.config.SharedNetwork())
	if err := m.run(cmd); err != nil {
		ui.Printf("Warning: failed to remove network %s: %v\n", m.config.SharedNetwork(), err)
	}

	return nil
}

// Up starts the named projects in order, stopping at the first failure.
func (m *Manager) Up(names []string, opts UpOptions) error {
	for _, name := range names {
		project, err := m.Project(name)
		if err != nil {
			return err
		}
		if err := project.Up(opts); err != nil {
			return err
		}
	}
	return nil
}

// Down stops the named projects in order. A project that fails to stop does
// not keep the others from being stopped; all failures are reported together
// once every project has been tried.
func (m *Manager) Down(names []string, opts DownOptions) error {
	var errs []error
	for _, name := range names {
		project, err := m.Project(name)
		if err == nil {
			err = project.Down(opts)
		}
		errs = append(errs, err)
	}
	return errors.Join(errs...)
}
