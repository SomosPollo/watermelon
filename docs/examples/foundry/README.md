# Foundry Example

Sandbox configuration for Ethereum smart contract development with Foundry (Forge, Cast, Anvil).

## Why sandbox blockchain development?

- Dependencies pulled during `forge install` run arbitrary code
- Build scripts can access your private keys if stored on disk
- Malicious dependencies could exfiltrate wallet credentials

## Setup

```bash
cd your-foundry-project
cp /path/to/watermelon/docs/examples/foundry/.watermelon.toml ./
cp /path/to/watermelon/docs/examples/foundry/foundry.sh ./
watermelon config validate
watermelon run
```

## Inside the sandbox

```bash
sh ./foundry.sh forge build
sh ./foundry.sh forge test
sh ./foundry.sh anvil --host 0.0.0.0  # Local node on port 8545
```

The upstream image uses a shell entrypoint that drops arguments passed through
generic tool wrappers. The included helper selects `forge`, `cast`, `anvil`, or
`chisel` explicitly and mounts the current guest directory. Run it inside the
VM, from your project root. The configuration preloads the image during trusted
bootstrap; it does not create the generic Foundry wrappers.

The allowlist includes compiler download hosts for both x86-64 and ARM64 Linux.
Compiler files downloaded only into a temporary tool container are not retained
between helper invocations, so later commands may download them again.

## Connecting to testnets/mainnet

Merge your RPC provider into the existing allowlist, keeping the compiler
hosts, then apply the change with `watermelon rebuild`:

```toml
[network]
allow = [
    "github.com",
    "*.githubusercontent.com",
    "eth-mainnet.g.alchemy.com",    # Alchemy
    "mainnet.infura.io",            # Infura
    "eth.llamarpc.com",             # Llama
]
```

Then use inside sandbox:

```bash
sh ./foundry.sh forge script script/Deploy.s.sol --rpc-url "$ALCHEMY_URL"
```

Set `ALCHEMY_URL` inside the guest first; host environment variables are not
inherited. This command simulates the script. Broadcasting needs separate
wallet configuration and is outside this example.

## Contract verification

For Etherscan verification:

```toml
[network]
allow = [
    # ... existing entries
    "api.etherscan.io",
    "api-sepolia.etherscan.io",
]
```

## Private keys

**Never expose valuable private keys to untrusted dependencies.** The sandbox isolates unmounted host files and blocks non-allowlisted external traffic, but project files are read-write and destinations permitted by policy remain reachable. Prefer hardware wallets or disposable development keys.
