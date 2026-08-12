# Monorepo Example

Sandbox configuration for full-stack monorepos with multiple languages. The included `.watermelon.toml` uses the default project mount and strict `fail` network policy.

## Option A: Mount the project (default)

```bash
cd your-monorepo
cp /path/to/watermelon/docs/examples/monorepo/.watermelon.toml ./
watermelon run
```

The repository is mounted read-write at `/project`, which is also the default shell, exec, and tool-container workdir.

## Inside the sandbox

```bash
# Frontend
cd frontend
npm install
npm run dev &

# Backend
cd ../backend
python -m venv venv
source venv/bin/activate
pip install -r requirements.txt
python manage.py runserver 0.0.0.0:8000
```

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

## Running multiple services

Use backgrounding or a process manager inside the sandbox:

```bash
# Option 1: Background processes
npm run dev --prefix frontend &
python backend/manage.py runserver 0.0.0.0:8000 &

# Option 2: Use tmux inside sandbox
tmux new-session -d -s frontend 'npm run dev --prefix frontend'
tmux new-session -d -s backend 'cd backend && python manage.py runserver'
```

## Option B: Keep the host project unmounted

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
