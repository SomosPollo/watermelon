# Monorepo Example

Sandbox configuration for full-stack monorepos with multiple languages. The included `.watermelon.toml` uses the default project mount and strict `fail` network policy.

## Automatic setup

For a public untrusted repository, keep source in a managed guest workspace:

```bash
watermelon clone https://github.com/owner/monorepo
cd monorepo
watermelon exec --cwd frontend npm test
watermelon exec --cwd backend python -m pytest
```

For an existing local checkout, generate the configuration from its manifests:

```bash
cd your-monorepo
watermelon init
# Review .watermelon.toml, including network rules and forwarded ports
watermelon config validate
watermelon up
```

Automatic detection prepares supported languages in subdirectories with shared
toolchains and separate project dependencies. The local checkout is mounted
read-write. See [Project workflows](../../WORKSPACES.md) for version selection,
workspace handling, and discovery limits.

## Run services

For a Node frontend and Django backend, add these ports to the host
configuration before `up`:

```toml
[ports]
forward = [3000, 8000]
```

If the VM already exists, apply the change with `watermelon rebuild` first.
Then use separate host terminals for the two services:

```bash
# Terminal 1: the frontend must listen on port 3000
watermelon exec --cwd frontend npm run dev
```

```bash
# Terminal 2
watermelon exec --cwd backend python manage.py runserver 0.0.0.0:8000
```

Open `http://127.0.0.1:3000` and `http://127.0.0.1:8000` on the host. For Vite,
use its port `5173` in both the app command and the forward instead. Automatic
setup creates the backend's Python environment; do not create another one in
the guest shell.

## Typical monorepo structure

```
myapp/
├── .watermelon.toml
├── frontend/          # React/Next.js
│   ├── package.json
│   └── src/
├── backend/           # Django/FastAPI
│   ├── requirements.txt
│   └── app/
└── shared/            # Shared types/utils
```

## Manual configuration

The included `.watermelon.toml` is an alternative starting point for custom
manual tools. It does not enable the automatic setup used above. Manual
wrappers start separate containers at the configured VM workdir: changing the
guest shell's directory or activating a Python environment does not reconfigure
those wrappers. For Node commands in a child directory, use an explicit option
such as `npm --prefix frontend install`. Use automatic setup for the combined
frontend/backend workflow in this guide.

## Keep the host project unmounted

Use no-mount mode when you want to copy only selected content into a persistent, project-owned VM. This is not a shared global VM: Watermelon binds the fixed name to the directory containing this configuration and rejects collisions or lifecycle commands from another project.

```toml
[vm]
name = "monorepo-build"
image = "ubuntu-24.04"
mount_project = false
workdir = "/home/watermelon/work"

[network]
allow = [
    "registry.npmjs.org",
    "pypi.org",
    "files.pythonhosted.org",
]

[tools]
"node:20-slim" = ["node", "npm", "npx"]
"python:3.12-slim" = ["python", "python3", "pip"]

[provision]
scripts = ["./vm/setup.sh"]

[resources]
memory = "8GB"
cpus = 4
disk = "20GB"

[security]
enforcement = "fail"

[ide]
command = "cursor"
# Optional IDE-only override; otherwise vm.workdir is used.
# workdir = "/home/watermelon/work/frontend"
```

Create `vm/setup.sh` so the configured workdir exists before commands enter it:

```bash
#!/bin/sh
set -eu
install -d -o watermelon -g watermelon -m 0755 /home/watermelon/work
```

Keep this current-user-owned host script at the configured path while the VM exists. Watermelon rereads its exact bytes for status and before policy-checked execution; moving, deleting, or changing it prevents the VM from being treated as current.

Then create the VM and copy only the directories you want to expose:

```bash
watermelon run --no-shell
watermelon copy -r ./frontend monorepo-build:/home/watermelon/work/
watermelon copy -r ./backend monorepo-build:/home/watermelon/work/
watermelon run
```

There is no `/project` reference in the generated mount or wrappers. Because `vm.workdir` is configured, Node and Python tool containers bind `/home/watermelon/work` at that same path. If `vm.workdir` were omitted, wrappers would instead use the guest directory from which they are invoked.

Run normal lifecycle commands from this configured project directory or any
descendant within its Git, filesystem, and trusted-directory discovery
boundaries. Because `[vm].name` already selects the fixed name, the
`--name monorepo-build` flag is optional. `run`, `exec`, `code`, `status`, and
`logs` still require the valid discovered configuration for explicit-name use.
If the config becomes invalid, the ownership-verified recovery paths remain
available through `stop` and `destroy --name monorepo-build`.
