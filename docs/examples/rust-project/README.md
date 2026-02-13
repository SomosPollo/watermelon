# Rust Example

Sandbox configuration for Rust development with Cargo.

## Why sandbox Rust?

Rust crates can include `build.rs` scripts that run arbitrary code during compilation. A malicious crate could:
- Read sensitive files during build
- Exfiltrate data via network during compilation
- Inject malware into the build output

Watermelon ensures build scripts run in isolation.

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
"~/.cargo/registry" = { target = "/home/dev/.cargo/registry" }
```

Note: This gives build scripts read access to cached crates. For maximum security, don't share this mount.
