# Contributing to Portage

Thank you for your interest in contributing. This document covers everything you need to get started.

---

## Scope: Community vs. Enterprise

Portage is an **open-core** platform. The community edition (this repository) includes:

- The Kubernetes Operator and 10-phase state machine reconciler (`tools/portage/controllers/`)
- The Unified CLI (`dryrun`, `dashboard`, `ghostwire`, `operator`)
- The AST API translation engine (`tools/portage/pkg/ast/`)
- The pluggable Driver SPI registry and community baseline drivers (`tools/portage/pkg/driver/`)
- The CSI snapshot orchestrator (`tools/portage/pkg/snapshotmigrator/`)
- The pre-flight dry-run validator (`tools/portage/pkg/dryrun/`)
- The embedded observability dashboard (`tools/portage/pkg/dashboard/`)

The enterprise data-plane engines (Synapse-CDC continuous WAL replication, Ghostwire zero-RST TCP proxy) are **proprietary and closed-source**. Contributions to those components are not accepted here. See the [Open-Core Architecture section in the README](README.md#open-core-architecture--enterprise-edition) for the full feature split.

---

## Getting Started

### Prerequisites

- Go 1.22+
- Docker (for Kind clusters in integration tests)
- `kubectl` and `kind` (for full end-to-end testing)
- `make`

### Build

```bash
# Clone
git clone https://github.com/kunaldevxxx/Portage.sh.git
cd Portage.sh

# Build the unified CLI binary
go build -o bin/portage ./tools/portage

# Verify everything builds
cd tools/portage && go build ./...
```

### Run Tests

```bash
cd tools/portage

# Unit tests with race detection
go test -race -v ./...

# Vet and static analysis
go vet ./...
```

### Launch the Dashboard (Demo Mode)

```bash
./bin/portage dashboard -p 8080 --demo
# Open http://localhost:8080
```

### Run a Dry-Run Validation

```bash
./bin/portage dryrun -f demo/02-portage-migration.yaml
```

---

## How to Contribute

### 1. Find or File an Issue

Check the [issue tracker](https://github.com/kunaldevxxx/Portage.sh/issues) before starting work. For substantial changes, open an issue first to discuss the approach before writing code.

Good first issues are tagged [`good first issue`](https://github.com/kunaldevxxx/Portage.sh/labels/good%20first%20issue).

### 2. Fork and Branch

```bash
git checkout -b feat/your-feature-name
# or
git checkout -b fix/your-bug-description
```

Use a descriptive branch name. Prefix with `feat/`, `fix/`, `docs/`, or `test/`.

### 3. Make Your Changes

- Keep each PR focused on a single concern.
- Write or update tests for any changed behavior.
- Run `go vet ./...` and `go test -race ./...` before pushing.
- Follow existing code style — the codebase uses standard `gofmt` formatting.

### 4. Commit Messages

Use conventional commit format:

```
feat(ast): add support for HPA autoscaling/v2 translation
fix(quiesce): handle pg_backup_stop timeout on slow storage
docs(driver): clarify community CDC driver limitations
test(dryrun): add validation cases for missing secretRef
```

### 5. Open a Pull Request

- Target the `main` branch.
- Fill in the PR template fully.
- Link to the issue it resolves (`Closes #123`).
- CI must pass (Go vet, race-detected tests, build verification) before review.

---

## Implementing a Custom Driver

The most common community contribution is a new replication or cutover driver. Portage uses a pluggable Go Service Provider Interface.

### DataReplicationDriver

Implement the interface in `tools/portage/pkg/driver/driver.go`:

```go
type DataReplicationDriver interface {
    Name() string
    StartReplication(ctx context.Context, endpoint, streamID string) error
    VerifyState(ctx context.Context, endpoint, streamID string) (bool, string, error)
    GetLag(ctx context.Context, endpoint, streamID string) (int64, error)
    GetCurrentLSN(ctx context.Context, endpoint, streamID string) (uint64, error)
}
```

### TrafficCutoverDriver

```go
type TrafficCutoverDriver interface {
    Name() string
    ExecuteCutover(ctx context.Context, directorEndpoint, routeID, targetEndpoint string, drainTimeoutSec int32) error
    ExecuteRollback(ctx context.Context, directorEndpoint, routeID string, drainTimeoutSec int32) error
    CheckTargetHealth(ctx context.Context, directorEndpoint, targetEndpoint string) (bool, error)
    GetDrainProgress(ctx context.Context, directorEndpoint, routeID string) (int64, int64, error)
}
```

### Registering Your Driver

Register your driver on init so it is available when referenced by name in a `PortageMigration` spec:

```go
func init() {
    driver.GlobalRegistry().RegisterReplicationDriver(NewMyCustomDriver())
}
```

Reference it from a migration manifest:

```yaml
cdcReplication:
  driver: my-custom-driver
  endpoint: "http://my-service.namespace.svc:9000"
```

See `tools/portage/pkg/driver/community.go` and `tools/portage/pkg/cdc/client.go` for reference implementations.

---

## What We Will Not Merge

- Changes that replicate enterprise functionality (continuous WAL streaming, Merkle-tree verification, zero-RST TCP splicing).
- Code that introduces external telemetry, analytics, or outbound network calls not initiated by the operator or user.
- Breaking changes to the `PortageMigration` CRD schema without a deprecation path.
- Dependencies with licenses incompatible with BSL 1.1.

---

## Reporting Security Issues

Do not open a public GitHub issue for security vulnerabilities. Follow the process in [SECURITY.md](SECURITY.md).

---

## License

By contributing, you agree that your contributions are licensed under the same [BSL 1.1 license](LICENSE) that covers this project. Contributions become Apache 2.0 on the Change Date (October 1, 2028).
