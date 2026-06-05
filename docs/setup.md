# Local Development Setup

## Prerequisites

| Tool | Min version | Install |
| --- | --- | --- |
| Go | 1.26 | [go.dev/dl](https://go.dev/dl/) |
| Docker + Compose | 24+ | [docs.docker.com](https://docs.docker.com/get-docker/) |
| Python | 3.9+ | Required for MkDocs only |
| Make | Any | Pre-installed on macOS/Linux |

## Private Module Access

This service consumes private Go modules hosted in the `github.com/BCBP-SOLUTIONS-FZC-LLC/*` organization (such as `platform-events`, `platform-pgcommon`, and `platform-gincommon`).

To fetch these modules, configure Go to bypass the public proxy and checksum database:

```bash
go env -w GOPRIVATE=github.com/BCBP-SOLUTIONS-FZC-LLC/*
```

### GitHub Authentication

You must configure Git to authenticate against GitHub when fetching private modules:

**SSH key (recommended for local dev):**

```bash
git config --global url."ssh://git@github.com/".insteadOf "https://github.com/"
```

**Personal Access Token (for CI/CD or HTTPS):**
Add a classic or fine-grained GitHub PAT with read repository permissions:

```bash
git config --global credential.helper store
echo "https://x-access-token:<your-github-token>@github.com" > ~/.git-credentials
chmod 600 ~/.git-credentials
```

---

## First-time setup

```bash
# 1. Configure Go private module path
go env -w GOPRIVATE=github.com/BCBP-SOLUTIONS-FZC-LLC/*

# 2. Install dev tooling (sqlc, goose, buf, mockgen, golangci-lint)
make tools

# 3. Copy env template and fill in local values
cp .env.example .env

# 4. Start local infra (PostgreSQL 16 + Valkey 8)
make docker-up

# 5. Run database migrations
make migrate-up

# 6. Start the server
go run ./cmd/server
```

The server is ready when you see:

```sh
INFO  HTTP server starting         {"addr": ":8080"}
INFO  gRPC server starting         {"addr": ":9090"}
INFO  stub: SQS consumer started (no-op — AWS_USE_STUB=true)
INFO  stub: outbox relay started (no-op)
```

Verify with:

```bash
curl http://localhost:8080/healthz   # → {"status":"OK"}
curl http://localhost:8080/readyz    # → {"status":"OK"}
curl http://localhost:8080/metrics   # → Prometheus text
```

## AWS stubs

By default `AWS_USE_STUB=true` in `.env.example`. This activates no-op stub adapters for SNS (publisher) and SQS (consumer) so the service boots without any AWS credentials.

## Code generation

After editing `.proto` files or SQL query files, regenerate:

```bash
make generate       # runs buf (proto) + sqlc (queries)
make mock           # regenerates GoMock stubs for core/port interfaces
```

Generated files are gitignored — never commit them.

## Running tests

The service uses unit tests and integration tests. Integration tests spin up database/infrastructure dependencies dynamically using `testcontainers-go`, meaning a local running Docker daemon is required.

```bash
# 1. Pre-pull Docker images for testcontainers (one-time command to warm cache)
make tools-integration

# 2. Run unit tests with race detector and coverage
make test

# 3. Run integration tests (spins up Docker containers via testcontainers-go automatically)
make test-integration

# 4. Print unit test coverage summary
make cover

# 5. Open HTML coverage report in browser
make cover-html
```

## Docs

```bash
pip install -r requirements-docs.txt
make docs-serve          # live-reload at http://localhost:8001
make docs-build          # build static site to site/
```

## Environment variables and Make

The Makefile automatically loads `.env` if the file exists, so variables like `DATABASE_URL` are available to all targets without manually sourcing the file first. Copy the template once and all `make` commands pick it up:

```bash
cp .env.example .env   # do this once
make migrate-up        # DATABASE_URL is read automatically
```

Variables in `.env` override any existing shell environment values for the duration of the make process only.

## Useful make targets

```sh
make help
```
