# Hardhat Example

Sandbox configuration for Ethereum smart contract development with Hardhat.

## Why sandbox blockchain development?

npm packages run postinstall scripts with full system access. A malicious package could:
- Read private keys from `~/.ethereum` or project `.env` files
- Exfiltrate mnemonics to remote servers
- Install keyloggers or clipboard monitors

Watermelon ensures npm packages can't access your host system.

## Setup

```bash
cd your-hardhat-project
cp /path/to/watermelon/docs/examples/hardhat/.watermelon.toml ./
watermelon run
```

## Inside the sandbox

```bash
npm install
npx hardhat compile
npx hardhat test
npx hardhat node  # Start local node on port 8545
```

## Connecting to testnets/mainnet

Add your RPC provider to the allowlist:

```toml
[network]
allow = [
    "registry.npmjs.org",
    "github.com",
    "eth-mainnet.g.alchemy.com",    # Alchemy
    "mainnet.infura.io",            # Infura
    "polygon-rpc.com",              # Polygon
]
```

## Contract verification

For Etherscan verification:

```toml
[network]
allow = [
    # ... existing entries
    "api.etherscan.io",
    "api-sepolia.etherscan.io",
    "api-polygonscan.com",
]
```

## Environment variables

Your `.env` file lives in the project and is accessible inside the sandbox. This is intentional for development, but be aware:

- **DO** use `.env` for RPC URLs and API keys
- **DON'T** store private keys with real funds in `.env`
- **DO** use hardware wallets or separate hot wallets for deployments

```bash
# hardhat.config.js reads from .env inside sandbox
npx hardhat run scripts/deploy.js --network sepolia
```

## Testing with mainnet forks

Forking mainnet requires network access to your RPC provider:

```toml
[network]
allow = [
    "registry.npmjs.org",
    "eth-mainnet.g.alchemy.com",
]
```

```bash
npx hardhat node --fork https://eth-mainnet.g.alchemy.com/v2/YOUR_KEY
```
