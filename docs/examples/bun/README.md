# Bun Example

Sandbox configuration for JavaScript/TypeScript projects using `bun`.

## Setup

```bash
cd your-project
cp /path/to/watermelon/docs/examples/bun/.watermelon.toml ./
watermelon run
```

## Inside the sandbox

```bash
bun install
bun run dev
```

Visit your app from the host using the forwarded port (for example `http://localhost:3000` or `http://localhost:5173`).

## Notes

- Watermelon pulls the `oven/bun:1` tool image during trusted bootstrap, so Docker Hub does not need to be in the workload allowlist.

## Troubleshooting network allowlist

If installs fail, inspect network policy events and add only trusted destinations the project needs:

```bash
watermelon logs
```
