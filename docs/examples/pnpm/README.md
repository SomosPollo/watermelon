# pnpm Example

Sandbox configuration for JavaScript/TypeScript projects using `pnpm`.

## Setup

```bash
cd your-project
cp /path/to/watermelon/docs/examples/pnpm/.watermelon.toml ./
watermelon run
```

## Inside the sandbox

```bash
pnpm install
pnpm dev
```

Visit your app from the host using the forwarded port (for example `http://localhost:3000` or `http://localhost:5173`).

## Optional: preinstall global CLIs

If you want common CLIs available every time the VM is created:

```toml
[provision]
npm = ["pnpm@10.20.0", "typescript", "eslint"]
```

This uses `npm install -g ...` during provisioning and creates the command wrappers when the VM is built. Prefer this configuration over an ad-hoc global install: a newly installed executable is not automatically exposed in the guest command path.

## Notes

- The example provisions `pnpm@10.20.0` during VM creation, so `pnpm` is available directly. Match this pin to your project's required pnpm version before creating the VM; leaving it unpinned can select a major release with different installation behavior.
- Watermelon pulls the base `node` image during trusted bootstrap, so Docker Hub does not need to be in the workload allowlist.

If pnpm reports ignored dependency build scripts, review the listed packages
and use `pnpm approve-builds` to approve only the scripts your project needs,
then retry installation. Keep the resulting project settings so later installs
use the same decisions.

## Troubleshooting network allowlist

If installs fail, inspect network policy events and add only trusted destinations the project needs:

```bash
watermelon logs
```
