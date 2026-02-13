# Watermelon Examples

Real-world configuration examples for different project types and security scenarios.

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
| [audit-package](./audit-package/) | Safely inspect suspicious npm packages |

## Usage

Copy the `.watermelon.toml` from any example into your project:

```bash
cp docs/examples/react-app/.watermelon.toml ~/my-project/
cd ~/my-project
watermelon run
```

Then customize the network allowlist and tools for your specific needs.
