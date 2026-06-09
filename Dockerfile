# syntax=docker/dockerfile:1

# ---- Stage 1: build a static, stripped, version-stamped binary ----
FROM golang:1.23 AS build

WORKDIR /src

# Cache module downloads as their own layer. (No external deps today, but this
# keeps the build correct and fast if any are added later.)
COPY go.mod ./
RUN go mod download

COPY . .

# Build metadata, injected by CI via --build-arg.
ARG VERSION=dev
ARG COMMIT=none
ARG DATE=unknown

# CGO disabled => fully static binary that runs on distroless/static.
# -trimpath strips local paths; -ldflags "-s -w" drops debug/symbol tables and
# stamps the version metadata.
RUN CGO_ENABLED=0 GOOS=linux go build \
    -trimpath \
    -ldflags="-s -w -X main.version=${VERSION} -X main.commit=${COMMIT} -X main.date=${DATE}" \
    -o /out/gateway .

# ---- Stage 2: minimal, rootless runtime ----
# distroless/static-debian12 contains no shell, package manager, or libc —
# only CA certs, tzdata, and the nonroot user (UID/GID 65532). This minimizes
# the vulnerability footprint of the shipped image.
#
# For reproducible, supply-chain-hardened builds, pin this and the builder image
# by digest (e.g. gcr.io/distroless/static-debian12:nonroot@sha256:...).
FROM gcr.io/distroless/static-debian12:nonroot

ARG VERSION=dev
ARG COMMIT=none
ARG DATE=unknown

LABEL org.opencontainers.image.title="docker-engine-gateway-api" \
      org.opencontainers.image.description="Bearer-token authentication gateway for the Docker Engine API over a Unix socket" \
      org.opencontainers.image.source="https://github.com/azra026/docker-engine-gateway-api" \
      org.opencontainers.image.licenses="Apache-2.0" \
      org.opencontainers.image.version="${VERSION}" \
      org.opencontainers.image.revision="${COMMIT}" \
      org.opencontainers.image.created="${DATE}"

COPY --from=build /out/gateway /gateway

# Run unprivileged.
USER nonroot:nonroot

EXPOSE 8080

# The image has no shell/curl; the binary self-probes via its -healthcheck flag.
HEALTHCHECK --interval=30s --timeout=5s --start-period=5s --retries=3 \
    CMD ["/gateway", "-healthcheck"]

ENTRYPOINT ["/gateway"]
