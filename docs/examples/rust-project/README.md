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

## Speeding up builds

To share the cargo registry between sandbox sessions:

```toml
[mounts]
"~/.cargo/registry" = { target = "/mnt/watermelon/cargo-home/registry" }
```

Set `CARGO_HOME=/mnt/watermelon/cargo-home` when using this cache. This gives build scripts read access to cached crates; for maximum security, don't share this mount.
