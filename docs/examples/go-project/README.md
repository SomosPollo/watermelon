# Go Example

Sandbox configuration for Go development.

## Why sandbox Go?

While Go doesn't have postinstall scripts like npm, it does:
- Execute code generation (`go generate`)
- Run tests that might have side effects
- Download and compile dependencies with CGO

Watermelon ensures these operations can't affect your host.

## Setup

```bash
cd your-go-project
cp /path/to/watermelon/docs/examples/go-project/.watermelon.toml ./
watermelon run
```

## Inside the sandbox

```bash
go mod download
go build ./...
go test ./...
go run .
```

## Private modules

For private Go modules, you may need to add your Git hosting and set up authentication:

```toml
[network]
allow = [
    "proxy.golang.org",
    "github.com",
    "git.internal.company.com",  # Your private Git
]

[mounts]
# Mount deploy key (read-only)
"~/.ssh/deploy_key" = { target = "/home/dev/.ssh/id_rsa" }
```
