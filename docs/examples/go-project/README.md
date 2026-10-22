# Go Example

Sandbox configuration for Go development.

## Why sandbox Go?

While Go doesn't have postinstall scripts like npm, it does:
- Execute code generation (`go generate`)
- Run tests that might have side effects
- Download and compile dependencies with CGO

Watermelon keeps these operations away from unmounted host files and services. The project directory remains mounted read-write.

## Setup

```bash
cd your-go-project
cp /path/to/watermelon/docs/examples/go-project/.watermelon.toml ./
watermelon run
```

## Inside the sandbox

```bash
go mod download all
go build ./...
go test ./...
go run .
```

`go mod download all` fetches dependency archives and records their checksums.
The shorter `go mod download` can leave a new project without the `go.sum`
entries needed by the following build. If imports and `go.mod` are out of sync,
run `go mod tidy` inside the sandbox first and review its manifest changes.

## Private modules

This recipe covers public modules. Private modules need an explicit network
policy and authentication configured inside the Go tool container. A host key
mounted into the guest is not automatically mounted into that container, and
exporting `GIT_SSH_COMMAND` in the guest shell does not pass it through the
manual wrapper. See [manual tools](../../CONFIG_SPEC.md#tools) before designing
a private-module workflow; do not expose credentials assuming those steps are
automatic.
