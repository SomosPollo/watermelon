# Watermelon Examples

[Documentation](../README.md) · [Project workflows](../WORKSPACES.md)

These examples configure mounted host projects. Most use **manual tools**
through `[tools]` and sometimes install global CLIs through `[provision]`.
The Python examples enable automatic `[setup]` so Python, pip, and installed
console scripts share a project environment. Follow each example's setup steps.

For supported languages, start with `watermelon init` and `watermelon up`, or
`watermelon clone` for a public GitHub repository. Use the examples when you
need to configure tools or policy yourself. Do not replace a managed clone's
settings with an example: its workspace disk and ownership settings must be
preserved.

## Quick Reference

| Example | Use Case |
|---------|----------|
| [ai-coding-assistants](./ai-coding-assistants/) | Claude Code, Codex, Aider with per-process network isolation |
| [react-app](./react-app/) | Standard React/Vite development |
| [nextjs](./nextjs/) | Next.js with API routes |
| [pnpm](./pnpm/) | JavaScript/TypeScript with pnpm |
| [bun](./bun/) | JavaScript/TypeScript with Bun |
| [python-django](./python-django/) | Django web application |
| [python-ml](./python-ml/) | Machine learning with PyTorch/TensorFlow |
| [rust-project](./rust-project/) | Rust development with Cargo |
| [go-project](./go-project/) | Go development |
| [monorepo](./monorepo/) | Full-stack monorepo (Node + Python) |
| [foundry](./foundry/) | Ethereum smart contracts with Foundry |
| [hardhat](./hardhat/) | Ethereum smart contracts with Hardhat |
| [audit-package](./audit-package/) | Inspect suspicious npm packages under a strict policy |

## Usage

Choose an example, review its `.watermelon.toml`, and copy it into a local
project that does not already have a configuration. Replace the source path
with the absolute path to your Watermelon checkout:

```bash
cp /path/to/watermelon/docs/examples/react-app/.watermelon.toml ~/my-project/
cd ~/my-project
watermelon config validate
watermelon run
```

For an example with `[setup]`, run `watermelon up` on the host before `run`.
For a manual example, run its dependency commands inside the guest shell.
Use `exit` to return to the host. If a configuration already exists, merge only
the settings you need and keep automatic-setup restrictions in mind.

Customize network rules, tools, resources, and forwarded ports before the
first run. After VM-affecting changes, use `watermelon rebuild`; preserve any
needed VM-local files first. See [configuration](../CONFIG_SPEC.md) for field
details and [storage behavior](../WORKSPACES.md#inspect-recover-and-delete).

The shipped examples use the default strict `fail` policy: non-allowlisted outbound traffic is blocked and recorded as rate-limited policy events. Workload DNS is redirected to a managed resolver that answers only policy names; loopback, established response traffic, and scoped DHCPv4 lease traffic required by VM control networking remain available. The DHCP exception is not arbitrary external UDP access.
