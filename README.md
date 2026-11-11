<p align="center">
  <img src="assets/logo.compressed.png" alt="Ifrit" width="300" />
</p>

<p align="center">
  A CLI tool that wraps Docker Compose to manage multiple subprojects with their own compose files, allowing them to be started/stopped on demand while sharing a common network.
</p>

![demo](assets/demo.gif)

## Features

- 🐳 Manage multiple Docker Compose projects from a single configuration
- 🔗 Share one Docker network across all projects, with no changes to your compose files
- 📦 Start and stop projects individually or all at once
- 📊 View status, and logs in an interactive viewer with one tab per service
- 🔧 Open shells and run commands in containers

## Installation

Requires Docker with the Compose V2 plugin (`docker compose`), and Go 1.27+ to build.

```bash
go install github.com/khueue/ifrit@latest
```

Or from source:

```bash
git clone https://github.com/khueue/ifrit.git
cd ifrit
sudo make install     # to /usr/local/bin
make install-user     # or to ~/bin, no sudo
```

## Quick Start

```bash
ifrit init      # create a sample ifrit.yml
ifrit up        # start all projects
ifrit status    # see what is running
ifrit logs      # watch the logs
ifrit down      # stop everything
```

## Configuration

`ifrit.yml` lists your projects:

```yaml
name_prefix: myapp-
implicit_networking: true

projects:
  database:
    path: ./database
  backend:
    path: ./backend
    compose_files:        # optional, defaults to [compose.yml]
      - compose.yml
      - compose.dev.yml
```

| Field | Required | Description |
|---|---|---|
| `name_prefix` | yes | Prefix for all Compose project names (`{name_prefix}{project}`) and for the shared network (`{name_prefix}shared`). No separator is added, so include one, as in `myapp-`. |
| `implicit_networking` | yes | When `true`, Ifrit puts every project on the shared network by itself. When `false`, you add the network to each compose file (see [Networking](#networking)). |
| `projects.<name>.path` | yes | Project directory, absolute or relative to the directory of `ifrit.yml`. |
| `projects.<name>.compose_files` | no | Compose files within the project directory. Defaults to `[compose.yml]`. |

Two environment variables take precedence over the file: `IFRIT_NAME_PREFIX` overrides `name_prefix`, and `IFRIT_SHARED_NETWORK` overrides the derived network name.

## Commands

```bash
# Start and stop
ifrit up                            # all projects
ifrit up backend frontend           # specific projects
ifrit up --recreate backend         # rebuild images, recreate containers
ifrit down                          # all projects; also removes the shared network
ifrit down backend
ifrit down --volumes backend        # also remove volumes

# Inspect
ifrit status                        # all projects and the shared network
ifrit logs                          # interactive viewer, one tab per service
ifrit logs backend frontend         # viewer for specific projects
ifrit logs --no-tui backend         # plain output
ifrit logs --no-tui -f backend      # plain output, following (one project)
ifrit logs --no-tui --tail 100 backend

# Shells and commands (starts the service if it is not running)
ifrit shell backend api             # bash if the container has it, otherwise sh
ifrit shell backend api -- ls -al
ifrit shell backend api -- "ls && echo done"
ifrit shell backend api -- env | grep PATH

# Other
ifrit init                          # create a sample ifrit.yml
ifrit version
ifrit <command> --help
```

Global flags: `-c, --config` for a config file other than `./ifrit.yml`, and `-v, --verbose` to print the underlying `docker` commands.

Notes:

- `ifrit logs` prints plain output by itself when piped or redirected, as with `--no-tui`.
- `ifrit shell` detects whether it runs in a terminal and only then attaches stdin and a TTY. Override with `--interactive=true|false`.
- `ifrit shell` exits with the exit status of the command it ran.

## Networking

Ifrit creates a Docker network named `{name_prefix}shared` on `ifrit up`, and removes it on `ifrit down`. Each project runs as its own Compose project on that network.

With `implicit_networking: true`, Ifrit passes a generated override file to Compose, so your compose files need no network configuration. With `false`, add this to each compose file; Ifrit sets the variable for you:

```yaml
networks:
  default:
    external: true
    name: ${IFRIT_SHARED_NETWORK}
```

### Talking to other projects

All containers are on the same network, so a service can reach services in other projects by **service name**, as long as that name is unique across projects. A name used in several projects (say, `app` in each) resolves to all of them, so which one you get is not defined.

To be unambiguous, use the **container name**. Compose generates it as `{name_prefix}{project}-{service}-1`, or you can set it yourself:

```yaml
# database/compose.yml
services:
  postgres:
    container_name: myapp-database
    image: postgres:15

# backend/compose.yml
services:
  api:
    image: node:18
    environment:
      DATABASE_URL: postgresql://myapp-database:5432/mydb
```

To see what is on the network:

```bash
docker network inspect myapp-shared
```

## Shell Completion

```bash
ifrit completion bash > /etc/bash_completion.d/ifrit
ifrit completion zsh > "${fpath[1]}/_ifrit"
ifrit completion fish > ~/.config/fish/completions/ifrit.fish
ifrit completion powershell | Out-String | Invoke-Expression
```

## Troubleshooting

- **Config file not found**: run from the directory holding `ifrit.yml`, or pass `-c /path/to/ifrit.yml`.
- **Network already exists, or lingers after a crash**: `ifrit down`, and if need be `docker network rm myapp-shared`.
- **Services cannot reach each other**: check that `implicit_networking` is `true` (or that each compose file has the network block above). If a service name exists in more than one project, address it by container name instead.

## Development

```bash
make build      # build to bin/ifrit
make test       # run the tests, with the race detector
make cover      # run the tests and fail if coverage drops below the minimum
make check      # fix, format and vet
make build-all  # build for multiple platforms
```

## License

MIT, see [LICENSE](LICENSE). Contributions are welcome.

## Why the name?

Ifrit? I honestly don't remember. I've been playing Lego The Hobbit with my son
recently and I first thought of something like "orc", as in orchestrator, which
made a lot of sense. Then I scrapped that for some reason and "ifrit" just came
to me. Besides, ifrits are hot as hell!
