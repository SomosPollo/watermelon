# Rust Example

Sandbox configuration for Rust development with Cargo.

## Why sandbox Rust?

Rust crates can include `build.rs` scripts that run arbitrary code during compilation. A malicious crate could:
- Read sensitive files during build
- Exfiltrate data via network during compilation
- Inject malware into the build output

Watermelon runs build scripts inside the VM, away from unmounted host files. This example also blocks new non-allowlisted external traffic and routes DNS through a resolver that answers only policy names. Loopback, established responses, and scoped VM-control DHCPv4 lease traffic remain available; the DHCP exception is not arbitrary external UDP access.

## Setup

```bash
cd your-rust-project
cp /path/to/watermelon/docs/examples/rust-project/.watermelon.toml ./
watermelon run
```

## Inside the sandbox

```bash
cargo build
cargo run
cargo test
```

## Build outputs and caches

Build outputs in the project's `target` directory remain on the mounted host
project. The manual Cargo wrapper does not inherit a guest-shell `CARGO_HOME`
or automatically expose additional guest mounts inside its tool container.

For persistent guest Cargo caches and automatic runtime selection, use an
automatic profile instead of copying the manual example. From a local project
that does not already have `.watermelon.toml`:

```bash
watermelon init --template rust
# Review the generated settings
watermelon up
watermelon exec cargo test --locked
```

See [runtime and cache behavior](../../WORKSPACES.md#runtime-and-dependency-support).
