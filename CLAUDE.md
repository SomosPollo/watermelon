# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Build & Development Commands

```bash
# Build the binary
go build -o watermelon ./cmd/watermelon

# Run unit tests
go test ./...

# Run a specific package's tests
go test ./internal/config/...

# Run E2E tests (requires Lima installed)
go test -tags=e2e ./test/...

# Run CLI during development
go run ./cmd/watermelon <command>
```

## Architecture Overview

Watermelon is a sandbox tool that isolates developer commands (npm install, pip install, etc.) inside a Lima-managed Linux VM on macOS, protecting the host from untrusted packages.

### Package Structure

- **cmd/watermelon/** - Cobra CLI entry point
- **internal/cli/** - Command implementations (init, run, exec, stop, destroy, status, list, logs)
- **internal/config/** - `.watermelon.toml` parsing, defaults, and validation
- **internal/lima/** - Lima VM lifecycle management and YAML config generation
- **internal/logs/** - Network policy logging

### Core Flow

```
User command → Cobra CLI → Load .watermelon.toml → Validate config → Generate Lima YAML → Execute via limactl
```

### Key Design Patterns

**VM Naming:** VMs are named `watermelon-{projectname}-{8char-sha256-hash}` derived from the project path for consistency.

**Policy-Driven Isolation:** All isolation rules (network allow-list, mounts, resources) are explicitly defined in config, not heuristic.

**Template-Based Generation:** Lima YAML configs are generated from Go templates with security validation to prevent shell injection.

**Command Handler Pattern:** Each CLI command has a `New<Command>Cmd()` function returning a configured Cobra command.

### Security Validation

Input validation is critical - the `internal/lima` package validates all user-provided strings (domains, ports) for shell metacharacters before template rendering to prevent injection attacks.

### Config Defaults

- Memory: 2GB, CPUs: 1, Disk: 10GB
- Enforcement: "log" (options: "log", "fail", "silent")
