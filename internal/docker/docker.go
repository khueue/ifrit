// Package docker runs Docker and Docker Compose commands for ifrit projects.
package docker

import (
	"bytes"
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

// composeCommand creates an exec.Cmd for "docker compose" with the given args.
func composeCommand(args ...string) *exec.Cmd {
	return exec.Command("docker", slices.Concat([]string{"compose"}, args)...)
}

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

// ExecExitError is returned by ComposeExec when the command it ran inside the
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

// getProject looks up a project by name, returning an error if not found.
func (m *Manager) getProject(projectName string) (config.Project, error) {
	project, ok := m.config.Projects[projectName]
	if !ok {
		return config.Project{}, fmt.Errorf("project %s not found in config", projectName)
	}
	return project, nil
}

// logCommand prints the full command line when verbose mode is enabled.
func (m *Manager) logCommand(cmd *exec.Cmd) {
	if !m.verbose {
		return
	}
	ui.Debugf("%s\n", strings.Join(cmd.Args, " "))
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

// composeArgs returns the common prefix args for a compose invocation:
//
//	--file <file1> --file <file2> ... --project-name <project_prefix>_<name>
//
// It also validates that every compose file exists on disk.
// When implicit networking is enabled, a generated override file is appended
// last so that the default network is set to the shared external network.
func (m *Manager) composeArgs(project config.Project, projectName string) ([]string, error) {
	var args []string
	for _, cf := range project.ComposeFiles {
		composePath := filepath.Join(project.Path, cf)
		if _, err := os.Stat(composePath); err != nil {
			return nil, fmt.Errorf("compose file not found at %s: %w", composePath, err)
		}
		args = append(args, "--file", composePath)
	}

	if *m.config.ImplicitNetworking {
		overridePath, err := m.ensureOverrideFile()
		if err != nil {
			return nil, err
		}
		args = append(args, "--file", overridePath)
	}

	args = append(args, "--project-name", fmt.Sprintf("%s%s", m.config.NamePrefix, projectName))
	return args, nil
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
	m.logCommand(cmd)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	if err := cmd.Run(); err != nil {
		return fmt.Errorf("failed to create network %s: %w", m.config.SharedNetwork(), err)
	}

	m.networkVerified = true
	return nil
}

// NetworkStatus runs docker network ls filtered by the shared network name,
// printing the result directly to stdout.
func (m *Manager) NetworkStatus() error {
	cmd := exec.Command("docker", "network", "ls", "--filter", fmt.Sprintf("name=^%s$", m.config.SharedNetwork()))
	m.logCommand(cmd)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	if err := cmd.Run(); err != nil {
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
	m.logCommand(cmd)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	if err := cmd.Run(); err != nil {
		ui.Printf("Warning: failed to remove network %s: %v\n", m.config.SharedNetwork(), err)
	}

	return nil
}

// ComposeUp runs docker compose up for a project.
func (m *Manager) ComposeUp(projectName string, forceRecreate bool) error {
	project, err := m.getProject(projectName)
	if err != nil {
		return err
	}

	if err := m.EnsureNetwork(); err != nil {
		return err
	}

	baseArgs, err := m.composeArgs(project, projectName)
	if err != nil {
		return err
	}

	ui.Printf("Starting project: %s\n", projectName)

	args := append(baseArgs,
		"up",
		"--remove-orphans",
	)

	if forceRecreate {
		args = append(args, "--build", "--force-recreate", "--always-recreate-deps")
	}

	args = append(args, "--detach")

	cmd := composeCommand(args...)
	cmd.Dir = project.Path
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Stdin = os.Stdin
	cmd.Env = m.composeEnv()

	m.logCommand(cmd)
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("failed to start project %s: %w", projectName, err)
	}

	return nil
}

// ComposeDown runs docker compose down for a project.
func (m *Manager) ComposeDown(projectName string, removeVolumes bool) error {
	project, err := m.getProject(projectName)
	if err != nil {
		return err
	}

	baseArgs, err := m.composeArgs(project, projectName)
	if err != nil {
		return err
	}

	ui.Printf("Stopping project: %s\n", projectName)

	args := append(baseArgs, "down")

	if removeVolumes {
		args = append(args, "--volumes")
	}

	cmd := composeCommand(args...)
	cmd.Dir = project.Path
	cmd.Env = m.composeEnv()
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	m.logCommand(cmd)
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("failed to stop project %s: %w", projectName, err)
	}

	return nil
}

// ComposeStatus shows the status of a project.
func (m *Manager) ComposeStatus(projectName string) error {
	project, err := m.getProject(projectName)
	if err != nil {
		return err
	}

	baseArgs, err := m.composeArgs(project, projectName)
	if err != nil {
		return err
	}

	args := append(baseArgs, "ps")

	cmd := composeCommand(args...)
	cmd.Dir = project.Path
	cmd.Env = m.composeEnv()
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	m.logCommand(cmd)
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("failed to get status for project %s: %w", projectName, err)
	}

	return nil
}

// ComposeLogs shows logs for a project.
func (m *Manager) ComposeLogs(projectName string, follow bool, tail string) error {
	project, err := m.getProject(projectName)
	if err != nil {
		return err
	}

	baseArgs, err := m.composeArgs(project, projectName)
	if err != nil {
		return err
	}

	args := append(baseArgs, "logs")

	if follow {
		args = append(args, "--follow")
	}

	if tail != "" {
		args = append(args, "--tail", tail)
	}

	cmd := composeCommand(args...)
	cmd.Dir = project.Path
	cmd.Env = m.composeEnv()
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Stdin = os.Stdin

	m.logCommand(cmd)
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("failed to get logs for project %s: %w", projectName, err)
	}

	return nil
}

// ComposeServiceLogsCmd builds and returns an *exec.Cmd for tailing logs of a
// single service within a project, without executing it. The caller is
// responsible for managing the process lifecycle.
func (m *Manager) ComposeServiceLogsCmd(projectName, serviceName string, tail string) (*exec.Cmd, error) {
	project, err := m.getProject(projectName)
	if err != nil {
		return nil, err
	}

	baseArgs, err := m.composeArgs(project, projectName)
	if err != nil {
		return nil, err
	}

	args := append(baseArgs, "logs", "--follow")

	if tail != "" {
		args = append(args, "--tail", tail)
	}

	args = append(args, serviceName)

	cmd := composeCommand(args...)
	cmd.Dir = project.Path
	cmd.Env = m.composeEnv()

	return cmd, nil
}

// UpAll starts all projects in sorted order.
func (m *Manager) UpAll(forceRecreate bool) error {
	if err := m.EnsureNetwork(); err != nil {
		return err
	}

	for _, name := range m.config.GetProjects() {
		if err := m.ComposeUp(name, forceRecreate); err != nil {
			return err
		}
	}

	return nil
}

// DownAll stops all projects in sorted order. A project that fails to stop
// does not keep the others from being stopped; all failures are reported
// together once every project has been tried.
func (m *Manager) DownAll(removeVolumes bool) error {
	var errs []error
	for _, name := range m.config.GetProjects() {
		errs = append(errs, m.ComposeDown(name, removeVolumes))
	}

	return errors.Join(errs...)
}

// ensureServiceRunning ensures a specific service is up and running in a project.
// This is idempotent: if the service is already running, it's a no-op.
func (m *Manager) ensureServiceRunning(projectName, serviceName string) error {
	project, err := m.getProject(projectName)
	if err != nil {
		return err
	}

	if err := m.EnsureNetwork(); err != nil {
		return err
	}

	baseArgs, err := m.composeArgs(project, projectName)
	if err != nil {
		return err
	}

	args := append(baseArgs, "up", "--detach", serviceName)

	cmd := composeCommand(args...)
	cmd.Dir = project.Path
	cmd.Env = m.composeEnv()

	// Capture output instead of printing directly — only show on error.
	var outBuf, errBuf bytes.Buffer
	cmd.Stdout = &outBuf
	cmd.Stderr = &errBuf

	m.logCommand(cmd)
	if err := cmd.Run(); err != nil {
		// Show the captured output so the user can diagnose the failure.
		if outBuf.Len() > 0 {
			os.Stdout.Write(outBuf.Bytes())
		}
		if errBuf.Len() > 0 {
			os.Stderr.Write(errBuf.Bytes())
		}
		return fmt.Errorf("failed to start service %s in project %s: %w", serviceName, projectName, err)
	}

	return nil
}

// ComposeExec executes a command in a running container.
func (m *Manager) ComposeExec(projectName, serviceName string, command []string, interactive bool) error {
	project, err := m.getProject(projectName)
	if err != nil {
		return err
	}

	baseArgs, err := m.composeArgs(project, projectName)
	if err != nil {
		return err
	}

	// Ensure the service is up and running before exec.
	if err := m.ensureServiceRunning(projectName, serviceName); err != nil {
		return err
	}

	args := append(baseArgs, "exec")

	if !interactive {
		args = append(args, "--no-TTY")
	}

	args = append(args, serviceName)
	args = append(args, command...)

	cmd := composeCommand(args...)
	cmd.Dir = project.Path
	cmd.Env = m.composeEnv()
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	if interactive {
		cmd.Stdin = os.Stdin
	}

	m.logCommand(cmd)
	if err := cmd.Run(); err != nil {
		if exitErr, ok := errors.AsType[*exec.ExitError](err); ok {
			return &ExecExitError{Code: exitErr.ExitCode()}
		}
		return fmt.Errorf("failed to exec into service %s in project %s: %w", serviceName, projectName, err)
	}

	return nil
}

// ComposeServices lists all services in a project.
func (m *Manager) ComposeServices(projectName string) ([]string, error) {
	project, err := m.getProject(projectName)
	if err != nil {
		return nil, err
	}

	baseArgs, err := m.composeArgs(project, projectName)
	if err != nil {
		return nil, err
	}

	args := append(baseArgs, "config", "--services")

	cmd := composeCommand(args...)
	cmd.Dir = project.Path
	cmd.Env = m.composeEnv()
	m.logCommand(cmd)
	output, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("failed to list services for project %s: %w", projectName, outputError(err))
	}

	// One service name per line; names cannot contain whitespace.
	return strings.Fields(string(output)), nil
}
