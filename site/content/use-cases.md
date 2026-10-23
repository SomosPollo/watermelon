# What can you do with Watermelon?

Run tools that execute third-party code while limiting their access to your
machine. Use a guest workspace for public repositories, or mount an existing
local project to keep editing on the host.

## Install dependencies with a smaller host footprint

Keep a public repository and its dependencies on a guest disk:

```bash
watermelon clone https://github.com/owner/project
cd project
watermelon run
```

`clone` lets you review or customize setup before installing dependencies. You
can defer setup and return with `watermelon up`; see the
[clone workflow](../../docs/WORKSPACES.md#clone-a-public-project).
Automatic setup supports Node, Python, Go, Rust, JVM, .NET, Ruby, PHP, and C/C++,
including mixed-language projects. See [runtime support](../../docs/WORKSPACES.md#runtime-and-dependency-support).

## Use Codex or Claude Code with a local project

Install the bundled integration with `watermelon agents install`, then prepare
your project with `watermelon init` and `watermelon up`. Start a new assistant
session; its project commands should use `watermelon exec`.

The assistant edits local files normally. The integration guides behavior;
it does not intercept host commands. Follow the [assistant setup guide](../../docs/CODING_ASSISTANTS.md)
for existing configurations and version requirements.

## Give AI coding tools the network access they need

An assistant running **inside the VM** can have additional API access while
other tools use only the general allowlist. For example:

```toml
[network]
allow = ["registry.npmjs.org"]

[network.process]
codex = ["api.openai.com"]
```

This excerpt needs a manual tool configuration. The [AI coding assistants
example](../../docs/examples/ai-coding-assistants/README.md) includes installation
and wrappers; use the configured wrapper for its process rules to apply.

## Inspect unfamiliar packages

Download a package archive without executing it, then inspect it with an empty
allowlist and strict enforcement. The [package inspection example](../../docs/examples/audit-package/README.md)
shows the process. Use a disposable directory without secrets: the default
project mount is writable. Inspection and isolation do not prove a package safe.

## Build, test, and serve a local project

Prepare your local project, then run its usual commands through Watermelon:

```bash
watermelon exec -- npm test
watermelon exec -- npm run build
```

For a dev server, [forward its port and bind to 0.0.0.0](../../docs/WORKSPACES.md#run-a-development-server).
Browse [examples](../../docs/examples/README.md) for React, Next.js, Django,
monorepos, machine learning, smart contracts, and other stacks.

## Adjust the sandbox as your project changes

When you need another allowed destination, a dev server port, or more memory,
edit the host policy:

```bash
watermelon config edit
```

Valid edits are saved after you close the editor. Watermelon reports whether
they need a rebuild; editor-only changes do not. Comments and custom settings
are retained when automatic setup updates the environment. Follow the
[settings workflow](../../docs/WORKSPACES.md#change-sandbox-settings) to apply
changes to an existing VM.

## Choose the right boundary

| Workflow | Source | Host access |
|----------|--------|-------------|
| `clone` a public GitHub repo | `/workspace/project` on a guest disk | No writable host mounts |
| `init` a local project | Host files mounted at `/project` | Entire project writable by default |

Review the host `.watermelon.toml` before running. Exposed files and allowed
network destinations remain accessible to the workload. Read the
[security model](../../docs/SECURITY.md) for the limits.
