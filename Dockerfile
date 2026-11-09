# syntax=docker/dockerfile:1

# The optional build_ca secret adds trusted CA certificates for downloads on
# TLS-inspecting networks (used by `make docker-smoke` on macOS). Secrets are
# not stored in the image; builds without it are unaffected.

# UI build (architecture-independent output).
FROM --platform=$BUILDPLATFORM node:22-alpine AS web
# A registry mirror can be passed for networks that block the public one.
ARG NPM_REGISTRY=https://registry.npmjs.org/
WORKDIR /src/web
COPY web/package.json web/pnpm-lock.yaml web/pnpm-workspace.yaml ./
RUN --mount=type=secret,id=build_ca,required=false \
    if [ -s /run/secrets/build_ca ]; then export NODE_EXTRA_CA_CERTS=/run/secrets/build_ca; fi; \
    npm_config_registry="$NPM_REGISTRY" npm install -g pnpm@11.8.0 \
 && echo "registry=$NPM_REGISTRY" > .npmrc && pnpm install --frozen-lockfile
COPY web/ ./
RUN pnpm build

# Pure-Go build (modernc SQLite needs no cgo), cross-compiled for the target.
FROM --platform=$BUILDPLATFORM golang:1.27-alpine AS api
ARG TARGETOS
ARG TARGETARCH
WORKDIR /src
COPY go.mod go.sum ./
RUN --mount=type=secret,id=build_ca,required=false \
    if [ -s /run/secrets/build_ca ]; then export SSL_CERT_FILE=/run/secrets/build_ca; fi; \
    go mod download
COPY cmd ./cmd
COPY internal ./internal
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH \
    go build -trimpath -ldflags="-s -w" -o /out/evcc-planner ./cmd/evcc-planner \
 && mkdir -p /out/data

# Minimal runtime: CA certificates, no shell, non-root (uid 65532). The app
# lives in /opt/evcc-planner so the data path matches the host's.
FROM gcr.io/distroless/static-debian12:nonroot
WORKDIR /opt/evcc-planner
COPY --from=api /out/evcc-planner /opt/evcc-planner/evcc-planner
COPY --from=api --chown=65532:65532 /out/data /opt/evcc-planner/data
COPY --from=web /src/web/dist /opt/evcc-planner/web
COPY config.yaml /opt/evcc-planner/config.yaml
ENV EVCC_PLANNER_LISTEN=0.0.0.0:7201 \
    EVCC_PLANNER_DATA_DIR=/opt/evcc-planner/data \
    EVCC_PLANNER_WEB_DIR=/opt/evcc-planner/web
EXPOSE 7201
VOLUME ["/opt/evcc-planner/data"]
HEALTHCHECK --interval=30s --timeout=5s --start-period=10s --retries=3 \
  CMD ["/opt/evcc-planner/evcc-planner", "healthcheck"]
ENTRYPOINT ["/opt/evcc-planner/evcc-planner", "-config", "/opt/evcc-planner/config.yaml", \
  "-local", "/opt/evcc-planner/data/config.local.yaml", "-env", "/opt/evcc-planner/data/.env"]
