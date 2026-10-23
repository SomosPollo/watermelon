# A sandbox for the work you already do

Watermelon runs development commands in a Lima-managed Linux VM. It is useful when a tool needs to execute third-party code, but does not need access to the rest of your machine.

## Install dependencies with a smaller host footprint

Package installation can run lifecycle scripts and other third-party code. Use a managed workspace to keep a public repository and its dependencies on a guest disk, with no writable host mounts.

```bash
watermelon clone https://github.com/owner/project
cd project
watermelon run
```

Replace the URL with a public GitHub repository. `clone` displays a setup plan and asks before installing dependencies. To inspect the source first, pass `--no-setup`. First-time VM creation downloads images and can take several minutes.

Automatic setup supports Node, Python, Go, Rust, JVM, .NET, Ruby, PHP, and C/C++, including mixed-language projects. See [project workflows](../../docs/WORKSPACES.md) for supported runtimes, version selection, and storage behavior.

## Use Codex or Claude Code with a local project

Keep your assistant on the host and use Watermelon for project execution.
Install the bundled skill, then add guidance to the project's `AGENTS.md` and
`CLAUDE.md` without replacing existing instructions:

```bash
watermelon agents install --agent codex,claude
cd your-project
watermelon init
# Review the generated policy and setup plan before approval.
watermelon config validate
watermelon up
```

For a project that already has `.watermelon.toml`, use
`watermelon init --instructions-only` instead of `init`. Start a new assistant
session and give it a normal development task; its project commands should
use `watermelon exec`.

The integration commands were added after v0.5.0. See the
[assistant setup guide](../../docs/CODING_ASSISTANTS.md) for version requirements,
installation conflicts, and execution checks. Host assistant API connections
do not require guest allowlist entries. Skills guide the assistant; only
commands routed through Watermelon execute in the VM.

## Give AI coding tools the network access they need

For an assistant installed inside the VM, its API and a package manager's registry need different network access. Configure general network rules and additional per-process rules to separate those needs.

```toml
[network]
allow = ["registry.npmjs.org"]

[network.process]
codex = ["api.openai.com"]
claude = ["api.anthropic.com", "*.anthropic.com"]
```

This is a policy excerpt, not a complete configuration. The [AI coding assistants example](../../docs/examples/ai-coding-assistants/README.md) includes provisioning and command wrappers. Run the configured wrapper so its process-specific network policy applies. Allowed destinations can still carry data out of the VM; keep credentials and mounts limited to what the task requires.

For host assistants that should route project commands through Watermelon, use
the [assistant setup guide](../../docs/CODING_ASSISTANTS.md).

## Inspect unfamiliar packages

Download an exact package archive without executing it, then inspect its contents in a VM with an empty network allowlist. The [strict package inspection example](../../docs/examples/audit-package/README.md) walks through the complete process.

```toml
[network]
allow = []

[security]
enforcement = "fail"
```

In `fail` mode, connections outside the allowlist are blocked and rate-limited policy events are recorded. Managed DNS answers only policy names. Loopback, established responses, and scoped VM-control DHCPv4 traffic remain available. The example mounts the inspection directory read-write, so use a disposable directory without secrets.

Code inspection and a sandbox reduce exposure; neither proves a package is safe. Read the [security model](../../docs/SECURITY.md) before running hostile code.

## Build, test, and serve a local project

Keep editing your existing project on the host while running its tools inside the guest. The default local workflow mounts the entire project read-write at `/project`.

```bash
cd your-project
watermelon init
# Review .watermelon.toml before continuing
watermelon config validate
watermelon up
watermelon exec -- npm test
```

Use your project's test command. For a development server, configure a forwarded port and bind the guest server to `0.0.0.0`. See [running a development server](../../docs/WORKSPACES.md#run-a-development-server), or start from the [React/Vite](../../docs/examples/react-app/README.md), [Next.js](../../docs/examples/nextjs/README.md), or [Django](../../docs/examples/python-django/README.md) examples.

## Choose the right boundary

| Workflow | Where source lives | Host project access |
|---|---|---|
| Public repository with `clone` | Persistent guest disk at `/workspace/project` | No writable host mounts |
| Existing project with `init` | Host directory mounted at `/project` | Entire project is read-write by default |

Treat `.watermelon.toml` as trusted host policy. Review changes to mounts, network rules, runtime images, and provisioning scripts before applying them. A VM rebuild retains the managed workspace disk, including any malicious files already on it.

Watermelon relies on the VM boundary, does not protect against VM escapes, and cannot prevent exfiltration through destinations your policy allows. The [security model](../../docs/SECURITY.md) explains the limits; the [configuration reference](../../docs/CONFIG_SPEC.md) explains the controls.
