# Development

[Documentation](README.md)

Use Go 1.27.3 or newer (including current standard-library security fixes).
Run the commands below from a trusted Watermelon source checkout.

## Build and run

```bash
go build -o watermelon ./cmd/watermelon
./watermelon --help
# Or run directly during development:
go run ./cmd/watermelon --help
```

For an `ask`-mode source build, also
[build the matching Linux sidecar](INSTALLATION.md#the-ask-mode-network-interceptor).

## Unit tests and static checks

```bash
go test ./...
go test -race ./...
go vet ./...
# Run one package while iterating:
go test ./internal/config/...
```

The package suite checks config and project detection, generated policy, host
state, authenticated verdicts, and CLI lifecycle orchestration. CLI and Lima
tests that replace package-level command seams or change process environment
must remain serial; restore overrides with `t.Cleanup`.

`go test ./test -run TestAgentIntegrations -v` builds the real CLI and tests
Codex and Claude Code installation, reinstallation, project adoption, and
preservation of custom content in isolated user directories. It requires
neither assistant credentials nor Lima. It verifies integration files and CLI
behavior, not live model compliance with instructions.

## Lima and VM tests

Install the [Lima prerequisites](INSTALLATION.md#requirements), then choose the
checks you need:

```bash
# Installed-Lima contracts; parses config without booting a VM
WATERMELON_TEST_REAL_LIMA=1 go test -count=1 ./internal/lima
# CLI workflows without a VM; requires Lima
go test -tags=e2e -short -count=1 ./test/...
# Full CLI and VM tests; Linux normally requires usable /dev/kvm
go test -tags=e2e ./test/...
```

Real Lima parsing and templating checks are opt-in, so installing Lima does
not change ordinary unit-test behavior. The CI Lima compatibility matrix
enables them for every supported boundary version, and an explicit opt-in
fails if Lima is missing. `WATERMELON_TEST_REAL_LIMA_INFO=1` remains a legacy
alias. Full VM E2E tests verify the actual filesystem, privilege, networking,
and persistence boundaries; generated-script assertions alone cannot prove
those boundaries.

Managed-workspace coverage can also be run separately:

```bash
go test -tags=e2e -run '^TestE2EManagedWorkspaceProjects$' -v ./test/...
go test -tags=e2e -run '^TestE2EManagedWorkspaceRuntime$' -v ./test/...
go test -tags=e2e -run '^TestE2EManagedWorkspaceMultiLanguage$' -v ./test/...
go test -tags=e2e -run '^TestE2EExtendedLanguages$' -timeout=65m -v ./test/...
go test -tags=e2e -run '^TestE2ECloneCustomizationAndConfigEdit$' -timeout=50m -count=1 -v ./test/...
```

The first tests live public GitHub acquisition. The second uses controlled
guest-only source fixtures to isolate runtime, policy, rebuild, forwarding,
and export checks from GitHub transport availability. The third verifies all
four languages together, cross-language hooks, nested dependencies, restart
and rebuild persistence. The extended test adds JVM, .NET, Ruby, PHP, C/C++,
pnpm/Yarn, and uv/Poetry in one VM. All require real VMs
and network access for trusted images and package registries.

The clone/config workflow test uses Python 3's standard-library PTY support to
drive the real setup prompt. It clones a small public Node project into an
isolated Lima home, checks deferred and approved customizations, validates
editor drafts, updates the runtime while retaining comments and custom policy,
then edits and rebuilds a forwarded port and verifies guest HTTP access.
Its disposable VMs and workspace disk are cleaned up after the test.

Set `WATERMELON_E2E_ALLOW_TCG=1` to try the full Linux VM lifecycle under slow QEMU TCG when KVM is unavailable.
CI sets `WATERMELON_E2E_REQUIRE_REAL_VM=1` so missing prerequisites fail instead of silently skipping the security scenarios.

## Source layout

| Path | Purpose |
|------|---------|
| `cmd/watermelon/` | CLI entry point |
| `internal/cli/` | Command handlers and lifecycle orchestration |
| `internal/config/` | Configuration parsing, defaults, and validation |
| `internal/lima/` | VM management and Lima configuration generation |
| `internal/logs/` | Network policy logs |
| `test/` | CLI, VM, and installer checks |

The Lima configuration template lives in
`internal/lima/templates/lima.yaml.tmpl`. It and the helpers under
`internal/lima/runtime/` are embedded at build time; edit these source assets
directly without a separate generation step.

## Documentation website

The GitHub Pages website builds directly from the Markdown documentation in
this repository. For local preview, content organization, checks, and deployment
setup, see the [website contributor guide](../site/README.md).

```bash
cd site
npm ci
npm run dev
# Build and verify internal links, section anchors, search, and downloads:
npm run check
```

The website build requires Node.js 24 or newer. It does not change the Go
binary's build requirements.
