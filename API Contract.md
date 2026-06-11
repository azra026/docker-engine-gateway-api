# API Contract

This document describes the HTTP contract exposed by the **Docker Engine Gateway API** — a
dependency-free reverse proxy that places a bearer-token authentication layer in front of the
local Docker Engine API.

The gateway authenticates the caller with a single shared bearer token, strips the credential,
and transparently forwards every other request to the Docker daemon over its Unix socket. Apart
from `/healthz` and the auth/throttle layer, it defines **no API of its own** — the full Docker
Engine API surface is proxied unchanged.

---

## Base

| Property | Value |
|---|---|
| Default listen address | `0.0.0.0:8080` (port via `GATEWAY_PORT`) |
| Protocol | HTTP (terminate TLS in front, e.g. Caddy) |
| Base path | `/` (no prefix, no gateway versioning) |
| Upstream | Docker daemon Unix socket (`DOCKER_SOCKET`, default `/var/run/docker.sock`) |

---

## Authentication

All endpoints **except** `/healthz` require a bearer token.

```
Authorization: Bearer <GATEWAY_AUTH_TOKEN>
```

- Scheme keyword is case-insensitive (`Bearer`, `bearer`, `BEARER`).
- The token is validated with a constant-time comparison (timing-attack resistant).
- The `Authorization` header is **removed** before the request is forwarded to the Docker daemon.
- The token never appears in access logs or error messages.

On a missing or invalid token the gateway responds `401` with:

```
WWW-Authenticate: Bearer realm="docker-engine-gateway"
```

---

## Endpoints

### `GET /healthz` — Liveness probe

Unauthenticated health check.

**Request:** no auth, no body.

**Response** `200 OK`, `Content-Type: application/json`:

```json
{ "status": "ok", "version": "<build-version>" }
```

> Not written to access logs by default. Set `GATEWAY_LOG_HEALTHZ=true` to log probes at debug level.

---

### `ANY /*` — Docker Engine API proxy

Every path other than `/healthz` is forwarded to the Docker daemon, for any HTTP method.

**Request:**
- `Authorization: Bearer <token>` header (required).
- Path, query, headers, and body are passed through unchanged to the daemon
  (e.g. `GET /version`, `GET /containers/json`, `POST /images/create`, `GET /v1.43/info`).
- Docker API version prefixes (`/v1.43/...`) are passed through as-is.

**Response:**
- The daemon's status code, headers, and body are returned unchanged.
- Streaming and hijacked connections (e.g. `GET /events`, `docker logs -f`, `docker attach`,
  `docker build`) are preserved.

**Examples:**

```bash
# Daemon version
curl -H "Authorization: Bearer $TOKEN" http://gateway:8080/version

# List containers
curl -H "Authorization: Bearer $TOKEN" http://gateway:8080/containers/json

# Stream events
curl -H "Authorization: Bearer $TOKEN" http://gateway:8080/events
```

---

## Error responses

All gateway-generated errors are JSON with a single `error` field and
`Content-Type: application/json`:

```json
{ "error": "<message>" }
```

| Status | When | Message | Extra headers |
|---|---|---|---|
| `401 Unauthorized` | Missing/invalid bearer token | `missing or invalid bearer token` | `WWW-Authenticate: Bearer realm="docker-engine-gateway"` |
| `429 Too Many Requests` | Too many failed auth attempts from a client | `too many failed authentication attempts; retry later` | `Retry-After: <seconds>` |
| `502 Bad Gateway` | Docker daemon unreachable | `upstream Docker daemon unavailable` | — |

Errors originating from the Docker daemon itself (e.g. `404` for an unknown container) are
passed through in the daemon's own format.

---

## Rate limiting (auth throttle)

Repeated failed authentications from the same client are throttled.

- Keyed by client IP (`RemoteAddr`), or the first hop of `X-Forwarded-For` when
  `GATEWAY_TRUST_PROXY=true`.
- After `GATEWAY_AUTH_MAX_FAILURES` (default `10`) failures within
  `GATEWAY_AUTH_FAILURE_WINDOW` (default `1m`), further requests get `429` with a `Retry-After`
  header until the window expires.
- A successful authentication resets the client's failure counter.

---

## Response headers

| Header | Endpoints | Description |
|---|---|---|
| `X-Request-Id` | all | Random 16-char hex request identifier, also emitted in access logs |

---

## Access logging

Structured JSON is written to stderr (one object per request):

```json
{
  "time": "2026-06-09T12:34:56.789Z",
  "level": "INFO",
  "msg": "request",
  "request_id": "6f8d2a7b4d7c4f2d",
  "method": "GET",
  "path": "/version",
  "status": 200,
  "bytes": 1234,
  "duration_ms": 7,
  "remote_addr": "203.0.113.10",
  "user_agent": "curl/8.7.1"
}
```

The bearer token is never logged.

---

## Configuration

| Variable | Default | Description |
|---|---|---|
| `GATEWAY_AUTH_TOKEN` | — | Shared bearer token (exactly one of this / `_FILE` is required) |
| `GATEWAY_AUTH_TOKEN_FILE` | — | Path to a file containing the token (preferred) |
| `GATEWAY_PORT` | `8080` | TCP listen port |
| `DOCKER_SOCKET` | `/var/run/docker.sock` | Docker daemon Unix socket path |
| `GATEWAY_AUTH_MAX_FAILURES` | `10` | Failed auth attempts before `429` |
| `GATEWAY_AUTH_FAILURE_WINDOW` | `1m` | Throttle window (Go duration syntax) |
| `GATEWAY_TRUST_PROXY` | `false` | Use `X-Forwarded-For` first hop as the throttle key |
| `GATEWAY_LOG_LEVEL` | `info` | `debug` \| `info` \| `warn` \| `error` |
| `GATEWAY_LOG_HEALTHZ` | `false` | Log `/healthz` probes (debug level) |

> Setting both `GATEWAY_AUTH_TOKEN` and `GATEWAY_AUTH_TOKEN_FILE` is an error.

---

## Scope & limitations

- **Single shared secret** — all-or-nothing access. No per-user tokens, RBAC, or request
  allow-listing. Any authenticated caller has full control of the Docker daemon (effectively root
  on the host).
- The gateway does **not** terminate TLS itself; run it behind a TLS-terminating reverse proxy in
  production.
