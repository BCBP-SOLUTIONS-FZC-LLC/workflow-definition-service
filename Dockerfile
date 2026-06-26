# syntax=docker/dockerfile:1.7
# workflow-definition-service
#
# Multi-stage build:
#   Stage 1 (builder) — compiles the binary with private module access via build secret.
#   Stage 2 (runtime) — distroless image; no shell, no package manager, minimal attack surface.
#
# Build (local, using SSH agent for private modules):
#   docker build \
#     --secret id=go_private_token,env=GO_PRIVATE_TOKEN \
#     -t workflow-definition-service:dev .
#
# Build (CI):
#   docker build \
#     --secret id=go_private_token,env=GO_PRIVATE_TOKEN \
#     --build-arg BUILD_VERSION=$(git describe --tags --always --dirty) \
#     -t workflow-definition-service:$TAG .

FROM golang:1.26-alpine AS builder

ARG BUILD_VERSION=dev
ENV CGO_ENABLED=0 \
    GOOS=linux \
    GOARCH=amd64 \
    GOPRIVATE=github.com/BCBP-SOLUTIONS-FZC-LLC \
    GONOSUMDB=github.com/BCBP-SOLUTIONS-FZC-LLC/*

WORKDIR /build

# git required for private module access
RUN apk add --no-cache git ca-certificates

RUN --mount=type=secret,id=go_private_token \
    git config --global credential.helper store && \
    echo "https://x-access-token:$(cat /run/secrets/go_private_token)@github.com" > ~/.git-credentials && \
    chmod 600 ~/.git-credentials

COPY go.mod go.sum ./
RUN go mod download && go mod verify

COPY . .

# Strip debug symbols to minimize binary size.
RUN go build \
    -ldflags="-w -s -X main.version=${BUILD_VERSION}" \
    -trimpath \
    -o /build/bin/server \
    ./cmd/server

# gcr.io/distroless/static-debian12: no shell, no libc, no package manager.
# Runs as nonroot (uid 65532) by default.
FROM gcr.io/distroless/static-debian12:nonroot

COPY --from=builder /build/bin/server /server

EXPOSE 8080 9090

# Requires the binary to be running; adjust interval to match Kubernetes probe config.
HEALTHCHECK --interval=30s --timeout=5s --start-period=10s --retries=3 \
    CMD ["/server", "-healthcheck"]

ENTRYPOINT ["/server"]
