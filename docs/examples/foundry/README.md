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
watermelon run
```

## Inside the sandbox

```bash
forge build
forge test
anvil  # Start local node on port 8545
```

## Connecting to testnets/mainnet

Add your RPC provider to the allowlist:

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
forge script script/Deploy.s.sol --rpc-url $ALCHEMY_URL --broadcast
```

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

```bash
# Pass key at runtime (not stored in files)
PRIVATE_KEY=0x... forge script script/Deploy.s.sol --private-key $PRIVATE_KEY
```
