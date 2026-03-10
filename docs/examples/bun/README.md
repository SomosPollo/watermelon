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

- The example allowlist includes Docker Hub domains so Watermelon can pull the `oven/bun:1` tool image.

## Troubleshooting network allowlist

If installs fail, check blocked domains and add only what you need:

```bash
watermelon logs
```
