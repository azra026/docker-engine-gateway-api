# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/)
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [1.0.0] - 2026-06-09

### Added

- Structured JSON access logging via `log/slog`.
- Per-client authentication throttling with `429` responses and `Retry-After`
  headers.
- `GATEWAY_AUTH_TOKEN_FILE` for file-based secret loading.
- `-version` and `-healthcheck` CLI flags.
- Container `HEALTHCHECK` support for the shell-less distroless image.
- CI workflow coverage for `vet`, `test`, `build`, and `govulncheck`.
- CodeQL analysis.
- GitHub Container Registry release workflow with SBOM and provenance.
- Cosign signing for release artifacts.
- Dependabot updates.
- OCI image labels.
- `NOTICE`, `CODE_OF_CONDUCT`, and contributor issue / pull request templates.

## [0.1.0]

### Added

- Initial release of the stdlib-only reverse proxy in front of `docker.sock`.
- Constant-time bearer-token authentication.
- `Authorization` header stripping before requests reach Docker Engine.
- Unix-socket transport to the local Docker daemon.
- Rootless distroless container image.
- Unauthenticated `/healthz` endpoint.
- Graceful shutdown handling.
- Apache-2.0 licensing and core project documentation.
