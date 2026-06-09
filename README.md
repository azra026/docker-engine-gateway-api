# Docker Engine API Gateway

[![CI](https://github.com/azra026/docker-engine-gateway-api/actions/workflows/ci.yml/badge.svg?branch=main)](https://github.com/azra026/docker-engine-gateway-api/actions/workflows/ci.yml)
[![CodeQL](https://github.com/azra026/docker-engine-gateway-api/actions/workflows/codeql.yml/badge.svg?branch=main)](https://github.com/azra026/docker-engine-gateway-api/actions/workflows/codeql.yml)
[![Release](https://github.com/azra026/docker-engine-gateway-api/actions/workflows/release.yml/badge.svg?branch=main)](https://github.com/azra026/docker-engine-gateway-api/actions/workflows/release.yml)
[![License: Apache 2.0](https://img.shields.io/badge/License-Apache_2.0-blue.svg)](./LICENSE)
[![Go Report Card](https://goreportcard.com/badge/github.com/azra026/docker-engine-gateway-api)](https://goreportcard.com/report/github.com/azra026/docker-engine-gateway-api)
[![Built with Go](https://img.shields.io/badge/Go-1.26%2B-00ADD8.svg)](https://go.dev)

A tiny, dependency-free reverse proxy that puts a **Bearer-token authentication
layer** in front of the local Docker Engine API. The Docker daemon socket
(`/var/run/docker.sock`) has no built-in authentication and is **root-equivalent**;
this gateway lets you expose the Docker API over the network behind a shared
secret, using only the Go standard library.

```
┌──────────┐   HTTPS + Bearer token    ┌────────────────────┐   unix://docker.sock   ┌──────────────┐
│  client  │ ─────────────────────────▶│  gateway (this app) │ ─────────────────────▶ │ Docker daemon │
└──────────┘   Authorization: Bearer …  └────────────────────┘   (token stripped)     └──────────────┘
```

## Features

- **Bearer-token auth** validated with `crypto/subtle.ConstantTimeCompare` (timing-attack resistant).
- **Token never leaks downstream** — the `Authorization` header is stripped in the proxy `Director` before reaching dockerd.
- **Per-client auth throttling** — repeated failures trigger `429` responses with `Retry-After`.
- **Structured JSON access logs** via `log/slog`, with per-request IDs and no token leakage.
- **Native Unix-socket transport** via a custom `http.Transport.DialContext` — no third-party HTTP/Docker clients.
- **Standard library only** — zero external dependencies (`go.mod` has no `require` block).
- **Unauthenticated `/healthz`** endpoint for container/orchestrator liveness probes.
- **Hardened server** — explicit header/idle timeouts, request size cap, graceful shutdown.
- **Minimal attack surface** — ships as a rootless [distroless](https://github.com/GoogleContainerTools/distroless) image.

## ⚠️ Security warning — read this first

> **Exposing the Docker socket is equivalent to handing out root on the host.**
>
> Anyone who can reach the Docker Engine API can start a privileged container,
> bind-mount `/`, and take over the machine. This gateway adds **authentication**
> — it does **not** sandbox or restrict what an authenticated caller can do.
>
> Operate it accordingly:
> - **Always** terminate TLS in front of this gateway. The recommended production
>   path is Caddy with the copy-pasteable `examples/Caddyfile` and
>   `examples/docker-compose.yml` below. The bearer token is sent in cleartext
>   over plain HTTP.
> - Use a **long, high-entropy** `GATEWAY_AUTH_TOKEN` (e.g. `openssl rand -hex 32`).
> - Restrict network exposure with a firewall / security group / network policy.
> - Treat an authenticated client as a host root user. Do not hand the token to
>   anything you would not trust with root.
> - Rotate the token periodically and on any suspected compromise.

This is a single-shared-secret gateway. Per-user tokens, RBAC, and request
allow-listing are **out of scope** (see [Roadmap](#roadmap)).

## Quickstart

### Run from source

```bash
# Requires Go 1.26+ and a reachable Docker socket.
export GATEWAY_AUTH_TOKEN="$(openssl rand -hex 32)"
go run .
```

```bash
# Liveness probe — no token required:
curl -s localhost:8080/healthz
# => {"status":"ok","version":"..."}

# Unauthenticated Docker call is rejected:
curl -s -o /dev/null -w '%{http_code}\n' localhost:8080/version
# => 401

# Authenticated call is proxied to the daemon:
curl -s -H "Authorization: Bearer $GATEWAY_AUTH_TOKEN" localhost:8080/version
# => {"Platform":{...},"Version":"...","ApiVersion":"...", ...}
```

### Run with Docker

```bash
docker build -t docker-engine-gateway-api .

docker run --rm \
  -p 8080:8080 \
  -e GATEWAY_AUTH_TOKEN="$(openssl rand -hex 32)" \
  -v /var/run/docker.sock:/var/run/docker.sock \
  docker-engine-gateway-api
```

> **Socket permissions:** the image runs as the unprivileged `nonroot` user
> (UID/GID `65532`). That user must have read/write permission on the mounted
> socket. On most hosts the socket is owned by the `docker` group, so add
> `--group-add "$(getent group docker | cut -d: -f3)"` to the `docker run`
> command (or set the equivalent in your orchestrator).

### Run from GHCR

```bash
docker pull ghcr.io/azra026/docker-engine-gateway-api:latest
```

### Caddy

For production, terminate TLS with Caddy and keep the gateway on the private
side of the network. The recommended deployment pair is
[examples/Caddyfile](./examples/Caddyfile) and
[examples/docker-compose.yml](./examples/docker-compose.yml).

```bash
docker compose -f examples/docker-compose.yml up -d
```

Keep `GATEWAY_TRUST_PROXY=true` when the gateway sits behind Caddy so the
auth-throttling key can use the trusted client IP from `X-Forwarded-For`
(Caddy sets that header by default).

## Configuration

All configuration is via environment variables.

| Variable | Required | Default | Description |
|---|---|---|---|
| `GATEWAY_AUTH_TOKEN` | one of these two | — | Shared bearer token clients must present. |
| `GATEWAY_AUTH_TOKEN_FILE` | one of these two | — | Path to a file containing the token (preferred; avoids env leakage). Mutually exclusive with `GATEWAY_AUTH_TOKEN`. |
| `GATEWAY_PORT` | No | `8080` | TCP listen port. |
| `DOCKER_SOCKET` | No | `/var/run/docker.sock` | Docker Engine Unix socket path. |
| `GATEWAY_AUTH_MAX_FAILURES` | No | `10` | Failed auths per window before `429`. |
| `GATEWAY_AUTH_FAILURE_WINDOW` | No | `1m` | Throttle window (Go duration). |
| `GATEWAY_TRUST_PROXY` | No | `false` | If true, key throttling on the first `X-Forwarded-For` hop (only safe behind a trusted terminator like Caddy). |
| `GATEWAY_LOG_LEVEL` | No | `info` | slog level: debug/info/warn/error. |
| `GATEWAY_LOG_HEALTHZ` | No | `false` | Log `/healthz` probes (at debug). |

### Secrets

`GATEWAY_AUTH_TOKEN_FILE` is the preferred way to provide the bearer token. The
file contents are trimmed of a trailing newline. Setting both
`GATEWAY_AUTH_TOKEN_FILE` and `GATEWAY_AUTH_TOKEN` is an error, and setting
neither is also an error so the process fails fast.

### Logging

Access logs are structured JSON on stderr via `log/slog`. A typical line looks
like this:

```json
{"time":"2026-06-09T12:34:56.789Z","level":"INFO","msg":"request","request_id":"6f8d2a7b4d7c4f2d","method":"GET","path":"/version","status":200,"bytes":1234,"duration_ms":7,"remote_addr":"203.0.113.10","user_agent":"curl/8.7.1"}
```

The bearer token is never logged. `/healthz` is omitted from access logs unless
`GATEWAY_LOG_HEALTHZ=true`.

### Authentication throttling

Repeated failed bearer-token checks from the same client eventually return
`429 Too Many Requests` with a `Retry-After` header. A successful request resets
that client state. When the gateway runs behind Caddy, set
`GATEWAY_TRUST_PROXY=true` so the throttling key can use the first
`X-Forwarded-For` hop rather than the proxy's address.

### Operations

- `-version` prints the version, commit, and build date, then exits.
- `-healthcheck` probes the local `/healthz` endpoint and exits `0` on success
  or `1` on failure. The distroless container image uses this for its shell-less
  `HEALTHCHECK`.

## How it works

1. Requests to `/healthz` return `200 {"status":"ok","version":"..."}` with no
   authentication.
2. Every other request must carry `Authorization: Bearer <GATEWAY_AUTH_TOKEN>`.
   The token is compared in constant time; failures return `401` with a
   `WWW-Authenticate: Bearer` challenge.
3. Authenticated requests are forwarded by `net/http/httputil.ReverseProxy`.
   A custom `http.Transport.DialContext` dials the Unix socket directly, so no
   TCP port on the daemon is required.
4. The proxy `Director` **deletes the `Authorization` header** before forwarding,
   so the daemon never receives the operator credential.

### A note on timeouts and streaming

Docker has long-lived streaming endpoints (`/events`, `docker logs -f`,
`attach`, `exec` hijack) and large slow uploads (`docker build`/`load`, image
push). A hard `ReadTimeout` or `WriteTimeout` would sever these, so the server
sets `ReadHeaderTimeout` and `IdleTimeout` while leaving read/write deadlines at
`0` so streaming requests stay open.

## Development

See [CONTRIBUTING.md](./CONTRIBUTING.md).

```bash
gofmt -l .          # formatting (should print nothing)
go vet ./...        # static analysis
go test ./... -race # unit + integration tests (no Docker required)
go build ./...      # compile
```

The test suite spins up a throwaway HTTP server on a temporary Unix socket, so
it runs in CI without a Docker daemon.

## Roadmap

- mutual TLS (mTLS) client authentication
- per-user / multiple tokens with rotation
- request method + path allow-listing (e.g. deny container `exec`)
- metrics

## Security

Found a vulnerability? Please **do not** open a public issue — follow the
private disclosure process in [SECURITY.md](./SECURITY.md).

## License

Licensed under the [Apache License, Version 2.0](./LICENSE).

Copyright 2026 James Roi Dela Cruz.
