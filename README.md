<p align="center">
  <img src="assets/logo.compressed.png" alt="Ifrit" width="300" />
</p>

<p align="center">
  A CLI tool that wraps Docker Compose to manage multiple subprojects with their own compose files, allowing them to be started/stopped on demand while sharing a common network.
</p>

## Features

- 🐳 Manage multiple Docker Compose projects from a single configuration
- 🔗 Automatically share a common Docker network across all projects
- 📦 Start/stop projects individually or all at once
- 📊 View status and logs across all projects
- 🔧 Execute commands and open shells in running containers
- 🔄 Modern Docker Compose (V2 plugin) support
- ⚙️ Simple YAML configuration

## Requirements

- Docker (20.10+)
- Docker Compose V2 (plugin)
- Go 1.26+ (for building from source)

**Note**: Ifrit requires the modern `docker compose` plugin (V2). The legacy standalone `docker-compose` (V1) is not supported.

## Installation

### From Source

```bash
git clone https://github.com/khueue/ifrit.git
cd ifrit
make build
sudo make install
```

### Using Go Install

```bash
go install github.com/khueue/ifrit@latest
```

## Quick Start

1. **Initialize a new configuration**:
   ```bash
   ifrit init
   ```

2. **Edit `ifrit.yml`** to configure your projects:
   ```yaml
   name_prefix: myapp
   shared_network: myapp_shared

   projects:
     database:
       path: ./database

     backend:
       path: ./backend

     frontend:
       path: ./frontend
   ```

3. **Update your `compose.yml` files** to use the shared network:
   ```yaml
   services:
     myservice:
       image: myimage:latest
       # ... other service configuration ...

   networks:
     default:
       external: true
       name: ${IFRIT_SHARED_NETWORK}
   ```

4. **Start all projects**:
   ```bash
   ifrit up
   ```

5. **View status**:
   ```bash
   ifrit status
   ```

## Configuration

The `ifrit.yml` file defines your projects and shared network:

```yaml
# Base name prefix - used as a prefix for all compose projects
name_prefix: myapp

# Shared Docker network name
shared_network: myapp_shared

# Define your projects
projects:
  database:
    path: ./database                    # Path to project directory
    compose_files:                      # Optional, defaults to [compose.yml]
      - compose.yml

  backend:
    path: ./backend

  frontend:
    path: ./frontend
```

### Configuration Fields

- **name_prefix** (required): Base name used to prefix all Docker Compose project names
- **shared_network** (required): Name of the shared Docker network
- **projects**: Map of project configurations
  - **path** (required): Relative or absolute path to the project directory
  - **compose_files** (optional): List of compose files (defaults to `[compose.yml]`)

### Environment Variable Overrides

The following environment variables can be used to override config values:

| Variable | Overrides | Description |
|---|---|---|
| `IFRIT_NAME_PREFIX` | `name_prefix` | Override the name prefix |
| `IFRIT_SHARED_NETWORK` | `shared_network` | Override the shared Docker network name |

Environment variables take precedence over values in `ifrit.yml`.

## Commands

### Project Management

```bash
# Start all projects
ifrit up

# Start specific projects
ifrit up backend frontend

# Force-recreate all containers from scratch
ifrit up --fresh backend

# Stop all projects (also removes the shared network)
ifrit down

# Stop specific projects
ifrit down backend frontend

# Stop and remove volumes
ifrit down --volumes backend
```

### Status and Monitoring

```bash
# Show status of all projects and the shared network
ifrit status

# View logs (all projects)
ifrit logs

# Follow logs for a specific project
ifrit logs -f backend

# Show last 100 lines
ifrit logs --tail 100 backend
```

### Container Access

```bash
# Open an interactive shell
ifrit shell backend api

# Execute a command in a container
ifrit shell backend api -- ls -al

# Run a database query
ifrit shell database postgres -- psql -U myuser -c "SELECT version()"

# Check environment variables
ifrit shell backend api -- env

# Run command non-interactively (for scripting)
ifrit shell --interactive=false backend api -- env > output.txt
```

### Other

```bash
# Initialize a new config file
ifrit init

# Show version
ifrit version

# Show help
ifrit --help

# Command-specific help
ifrit shell --help
```

## How It Works

1. **Shared Network**: Ifrit creates a Docker bridge network that all projects join. The network is created automatically on `ifrit up` and removed on `ifrit down`.
2. **Project Isolation**: Each project runs as a separate Docker Compose project with its own prefix (`{name_prefix}_{project_key}`)
3. **Environment Variables**: The `IFRIT_SHARED_NETWORK` variable is automatically passed to all `docker compose` commands

## Example Project Structure

```
myapp/
├── ifrit.yml
├── database/
│   ├── compose.yml
│   └── init.sql
├── backend/
│   ├── compose.yml
│   ├── Dockerfile
│   └── src/
└── frontend/
    ├── compose.yml
    ├── Dockerfile
    └── public/
```

### Example Docker Compose File

**backend/compose.yml**:
```yaml
services:
  api:
    build: .
    ports:
      - "8080:8080"
    environment:
      - DATABASE_URL=postgresql://db:5432/mydb

  db:
    image: postgres:15
    environment:
      - POSTGRES_DB=mydb
      - POSTGRES_PASSWORD=secret
    volumes:
      - db_data:/var/lib/postgresql/data

volumes:
  db_data:

networks:
  default:
    external: true
    name: ${IFRIT_SHARED_NETWORK}
```

**Note**: The `version:` field is deprecated in modern Docker Compose and should be omitted.

## Common Workflows

### Development Workflow

```bash
# Start your services
ifrit up

# Check everything is running
ifrit status

# Work on backend - follow logs
ifrit logs -f backend

# Need to access a container?
ifrit shell backend api

# Stop everything
ifrit down
```

### Working on Specific Services

```bash
# Only need the backend today?
ifrit up backend

# Stop frontend to work on it locally
ifrit down frontend

# Restart a service after changes
ifrit down backend
ifrit up backend
```

### Debugging

```bash
# Check what's running
ifrit status

# View recent logs
ifrit logs --tail 100 backend

# Follow logs in real-time
ifrit logs -f backend

# Open shell to investigate
ifrit shell backend api

# Run specific commands in a container
ifrit shell backend api -- ps aux
ifrit shell backend api -- env
ifrit shell backend api -- ls -la
```

## Network Communication

When using a shared external network, **container names** (not service names) are used for DNS resolution.

### Important: Use Container Names for Communication

Since projects use an external shared network, Docker Compose service names are only resolvable within their own project. To communicate across projects, you **must** use container names.

**Recommended approach - Set explicit container names:**

```yaml
# database/compose.yml
services:
  postgres:
    container_name: myapp_database
    image: postgres:15
    ports:
      - "5432:5432"

# backend/compose.yml
services:
  api:
    container_name: myapp_backend
    image: node:18
    environment:
      # Use the explicit container name from database project
      DATABASE_URL: postgresql://myapp_database:5432/mydb
    ports:
      - "8080:8080"

# frontend/compose.yml
services:
  web:
    container_name: myapp_frontend
    image: nginx
    environment:
      # Use the explicit container name from backend project
      API_URL: http://myapp_backend:8080
```

### Why This Matters

Without explicit container names, Docker Compose generates names automatically (like `myapp_backend_api_1`), making them hard to predict and reference.

**Service names only work within the same compose.yml file:**
- ✅ `database` → `postgres` works (same file)
- ❌ `backend` → `postgres` fails (different projects)
- ✅ `backend` → `myapp_database` works (explicit container name)

### Best Practices

1. **Always set `container_name`** for services that other projects need to access
2. **Use a naming convention**: `{name_prefix}_{role}` (e.g., `myapp_database`, `myapp_api`)
3. **Document container names** in your ifrit.yml comments

### Quick Reference

```bash
# Find container names on the network
docker network inspect myapp_shared

# Test connectivity from one container to another
ifrit shell backend api
# Inside container:
ping myapp_database
curl http://myapp_database:5432
```

## Tips

1. **Quick Shell Access**: Use `ifrit shell <project> <service>` instead of remembering full `docker compose` project names
2. **Run Commands**: Use `ifrit shell <project> <service> -- <command>` to run one-off commands
3. **Check Version**: Run `docker compose version` to verify your installation

## Building from Source

```bash
# Clone repository
git clone https://github.com/khueue/ifrit.git
cd ifrit

# Download dependencies
go mod download

# Build
make build

# Install (requires sudo)
sudo make install

# Or install to ~/bin (no sudo)
make install-user
```

### Build Targets

```bash
make build          # Build to bin/ifrit
make install        # Install to /usr/local/bin (requires sudo)
make install-user   # Install to ~/bin (no sudo)
make clean          # Remove build artifacts
make build-all      # Build for multiple platforms
```

## Shell Completion

Generate completion scripts for your shell:

```bash
# Bash
ifrit completion bash > /etc/bash_completion.d/ifrit

# Zsh
ifrit completion zsh > "${fpath[1]}/_ifrit"

# Fish
ifrit completion fish > ~/.config/fish/completions/ifrit.fish

# PowerShell
ifrit completion powershell | Out-String | Invoke-Expression
```

## Troubleshooting

### "Config file not found"

Make sure `ifrit.yml` exists in the current directory or specify the path:
```bash
ifrit -c /path/to/ifrit.yml up
```

### "Network already exists"

Stop all projects first, which will also clean up the network:
```bash
ifrit down
```

If the network is still lingering (e.g., from a previous crash), remove it manually:
```bash
docker network rm myapp_shared
```

### Services can't communicate

Verify all `compose.yml` files use the external network:
```yaml
networks:
  default:
    external: true
    name: ${IFRIT_SHARED_NETWORK}
```

### Check container names

```bash
docker ps
docker network inspect myapp_shared
```

## License

MIT License - See LICENSE file

## Contributing

Contributions are welcome! Please feel free to submit a Pull Request.

## Why the name?

Ifrit? I honestly don't remember. I've been playing Lego The Hobbit with my son
recently and I first thought of something like "orc", as in orchestrator, which
made a lot of sense. Then I scrapped that for some reason and "ifrit" just came
to me. Besides, ifrits are hot as hell!
