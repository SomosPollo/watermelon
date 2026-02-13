# Monorepo Example

Sandbox configuration for full-stack monorepos with multiple languages.

## Setup

```bash
cd your-monorepo
cp /path/to/watermelon/docs/examples/monorepo/.watermelon.toml ./
watermelon run
```

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
