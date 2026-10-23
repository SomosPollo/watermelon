# Use Codex or Claude Code with Watermelon

Keep your assistant on the host to edit local source. Use Watermelon for its
installs, builds, tests, scripts, and dev servers.

## Install the assistant skill

Run once on the host:

```bash
watermelon agents install
# Or choose explicitly:
watermelon agents install --agent codex,claude
```

The skill is bundled in the binary; no downloads or API credentials are needed.
If an older or customized skill conflicts, preserve it and move it aside before
reinstalling. Updating the CLI does not update installed skills. See
[install locations and options](COMMANDS.md#watermelon-agents-install).

## Prepare your local project

For a new Watermelon project, follow the [quick start](../README.md#quick-start):
`init`, review the config, then `config validate` and `up`.
`init` adds guidance to `AGENTS.md` and `CLAUDE.md`, preserving existing text.

For an existing configuration:

```bash
cd your-project
watermelon init --instructions-only
```

Review the initial `up` plan before approving dependency installation.

## Work with the assistant

Start a new session in the project and give it a normal development task.
Check that its execution uses commands such as:

```bash
watermelon exec -- npm run build
watermelon exec -- npm test
watermelon exec --cwd backend -- python -m pytest
```

Use `--cwd` for nested projects; changing only the host directory does not
select a guest subdirectory. Source edits use normal file tools. Host assistant
API connections stay on the host and need no guest allowlist entries.

Skills and project instructions guide the assistant; they do not intercept
its host shell or change its permissions. The VM can modify the mounted project.
If execution fails, diagnose it inside Watermelon; inspect `watermelon logs`
for network failures and review policy changes before applying them.

Managed clones keep source inside the guest, so edit them through guest commands
or a remote IDE. To install the assistant itself in the VM, use the
[AI coding assistants example](examples/ai-coding-assistants/README.md), including
its per-process network rules.

## Check your Watermelon version

If `watermelon agents install --help` is unknown, install a release containing
the integration or [build from source](DEVELOPMENT.md#build-and-run). These
commands were added after v0.5.0; releases may lag behind `main`. Ensure the
assistant can find the updated binary on `PATH`, then start a new session.
