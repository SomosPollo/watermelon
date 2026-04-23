# Monorepo Example

Sandbox configuration for full-stack monorepos with multiple languages.

## Two approaches

### Option A — Sandboxed project mount (default)

The standard approach: the repo is mounted into the VM and commands run against it.

```toml
[vm]
image = "ubuntu-22.04"

[network]
allow = [
    "registry.npmjs.org",
    "pypi.org",
    "files.pythonhosted.org",
    "github.com",
    "*.githubusercontent.com",
]

[tools]
"node:20-slim" = ["node", "npm", "npx"]
"python:3.12-slim" = ["python", "python3", "pip"]

[ports]
forward = [3000, 5173, 8000]

[resources]
memory = "8GB"
cpus = 4
disk = "20GB"

[security]
enforcement = "log"
```

```bash
cd your-monorepo
watermelon run

# Inside the sandbox — project is at /project
cd /project/frontend && npm install && npm run dev &
cd /project/backend && pip install -r requirements.txt && python manage.py runserver 0.0.0.0:8000
```

### Option B — Fixed-name VM, no project mount

Use this when the VM is the shared environment itself (e.g. a Docker host), not a per-project sandbox. Provision scripts set up everything; the repo is not bind-mounted.

```toml
[vm]
name = "my-dev-vm"
image = "ubuntu-24.04"
mount_project = false

[network]
allow = [
    "registry.npmjs.org",
    "github.com",
    "*.githubusercontent.com",
    "download.docker.com",
    "registry-1.docker.io",
    "auth.docker.io",
]

[resources]
memory = "8GB"
cpus = 4
disk = "50GB"

[provision]
scripts = ["./vm/setup.sh"]

[ide]
command = "cursor"
workdir = "/home/user"
```

Because the VM has a fixed name, you can target it from any directory:

```bash
watermelon run --name my-dev-vm
watermelon exec --name my-dev-vm "docker compose up -d"
watermelon stop --name my-dev-vm
```

Or omit `--name` entirely from any directory that contains this `.watermelon.toml`.

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
