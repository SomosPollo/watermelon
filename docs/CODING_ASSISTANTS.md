# Use Codex or Claude Code with Watermelon

[Documentation](README.md) · [Command reference](COMMANDS.md#watermelon-agents-install)

Keep Codex or Claude Code on the host, where it can edit your local project.
Install Watermelon's skill and project instructions so the assistant runs
dependency installs, builds, tests, scripts, and development servers in the VM.

## Check your Watermelon version

The integration commands were added after v0.5.0. Check your installed CLI:

```bash
watermelon agents install --help
```

If the command is unknown, use a release that includes it or build from `main`
with the [source build instructions](DEVELOPMENT.md#build-and-run). Put the
updated binary on the `PATH` used by your assistant. The release installer and
`go install ...@latest` select releases, which may lag behind `main`.

Install [Lima and Watermelon's other prerequisites](INSTALLATION.md#requirements),
then run `watermelon doctor` before preparing a project.

## Install the assistant skill

Run on the host:

```bash
watermelon agents install
# Or choose one or both explicitly:
watermelon agents install --agent codex,claude
```

The default detects installed Codex and Claude Code clients. Explicit selection
also works before their first launch. The skill is bundled in the Watermelon
binary; installation requires no downloads or assistant API credentials.

Identical installed skills are left untouched. If an older or customized copy
differs, preserve it and move it aside before reinstalling. See the
[command reference](COMMANDS.md#watermelon-agents-install) for install locations
and configuration-directory overrides. Updating the CLI alone does not update
an already installed skill.

## Prepare your local project

For a project without `.watermelon.toml`:

```bash
cd your-project
watermelon init
# Review the generated runtime, network rules, and mounts.
watermelon config validate
watermelon up
```

`init` adds marked guidance to `AGENTS.md` and `CLAUDE.md`, preserving existing
instructions, permissions, and Watermelon blocks. Review the initial setup
plan before approving installation. For manual tool configurations, follow
the [local project workflow](WORKSPACES.md#prepare-a-local-project).

For an existing Watermelon project, add the guidance without changing its
configuration:

```bash
cd your-project
watermelon init --instructions-only
```

## Give the assistant a normal development task

Start a new Codex or Claude Code session in the project. For example:

> Install the development dependencies, run the production build, and run the full test suite.

For an npm project, the assistant should use commands such as:

```bash
watermelon exec -- npm ci --include=dev
watermelon exec -- npm run build
watermelon exec -- npm test
```

Commands depend on the project's package manager and scripts. For a nested
project, use `watermelon exec --cwd frontend -- npm test`; changing only the
host working directory does not select a guest subdirectory.

Source edits use the assistant's normal file tools. The default local project
mount is read-write at `/project`. Host assistant API connections stay on the
host and need no extra guest network allowlist entries. To run the assistant
itself inside the guest, use the separate
[AI coding assistants example](examples/ai-coding-assistants/README.md).

## Check execution and diagnose failures

Check the assistant's reported commands for `watermelon exec`. Skills and
project instructions guide its behavior; they do not intercept its host shell.
The assistant's own permissions govern host access. See the
[security model](SECURITY.md) for the VM and mounted-file boundaries.

A build or test can fail even when Watermelon executes it correctly. Diagnose
the project error inside the VM. For network failures, inspect
`watermelon logs` and review any needed policy change before applying it. Do
not retry project commands on the host as a fallback.

If the skill is missing from a session, confirm its installation and start a
new session. If the assistant cannot find `watermelon`, check the binary and
its `PATH`. Keep the VM running between tasks unless you intend to stop it.
