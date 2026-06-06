# syntax=docker/dockerfile:1.7
###############################################################################
# mapexos/mapex-broker-mqtt — production-ready Mapex MQTT Broker image
#
# Bundles Mosquitto v2 + the in-house mapex-broker plugin (auth + ACL +
# presence + ingress, all in one .so). The envsubst entrypoint lets a
# deployment configure everything via environment variables — no file
# edits inside the container.
#
# Stages:
#   1. plugin-builder  — compiles mapex-broker.so via cgo
#   2. runtime         — debian:bookworm-slim base + mosquitto +
#                         the .so + envsubst entrypoint + config template
#
# Build (from repo root):
#   docker build -f docker/Dockerfile -t mapexos/mapex-broker-mqtt:dev .
#
# Or via the Makefile:
#   make build VERSION=2026.05.08
###############################################################################

###############################################################################
# Stage 1: plugin-builder
###############################################################################
FROM golang:1.25-bookworm AS plugin-builder

# libmosquitto-dev provides mosquitto.h + libmosquitto.so for the
# client API; mosquitto-dev provides the BROKER headers
# mosquitto_plugin.h + mosquitto_broker.h that the cgo entry needs.
# Both pin to bookworm's 2.x line so the plugin v5 ABI matches the
# runtime base image.
RUN apt-get update && \
    apt-get install -y --no-install-recommends \
        libmosquitto-dev \
        libmosquitto1 \
        mosquitto-dev \
        libssl-dev \
        ca-certificates && \
    rm -rf /var/lib/apt/lists/*

WORKDIR /build
COPY go.mod go.sum ./
RUN go mod download
COPY src/ ./src/

# `cgo_mosquitto_plugin` build tag activates the cgo entry point.
# GOWORK=off ignores any go.work in the build context so the plugin
# builds against its standalone go.mod.
RUN GOWORK=off CGO_ENABLED=1 \
    go build \
        -tags=cgo_mosquitto_plugin \
        -buildmode=c-shared \
        -o /out/mapex-broker.so \
        ./src

###############################################################################
# Stage 2: runtime
#
# debian:bookworm-slim base (not Alpine) because the plugin .so is
# compiled against glibc — running it on musl triggers
# `initial-exec TLS resolves to dynamic definition` at dlopen time.
# Debian's mosquitto 2.0.11 supports the 4 events the plugin registers
# (BASIC_AUTH, ACL_CHECK, DISCONNECT, MESSAGE).
###############################################################################
FROM debian:bookworm-slim

ARG IMAGE_VERSION=dev
ARG VCS_REF=unknown
ARG BUILD_DATE=unknown

LABEL org.opencontainers.image.title="Mapex MQTT Broker" \
      org.opencontainers.image.description="Mosquitto v2 + mapex-broker plugin (auth + ACL + presence + ingress, single .so)" \
      org.opencontainers.image.vendor="Mapex Solutions" \
      org.opencontainers.image.source="https://github.com/Mapex-Solutions/mapexMQTTBroket" \
      org.opencontainers.image.licenses="Proprietary" \
      org.opencontainers.image.version="${IMAGE_VERSION}" \
      org.opencontainers.image.revision="${VCS_REF}" \
      org.opencontainers.image.created="${BUILD_DATE}"

# Install mosquitto broker + runtime deps. gettext-base provides
# envsubst; netcat-openbsd provides `nc -z` for the healthcheck.
RUN apt-get update && \
    apt-get install -y --no-install-recommends \
        mosquitto \
        libmosquitto1 \
        gettext-base \
        netcat-openbsd \
        ca-certificates && \
    rm -rf /var/lib/apt/lists/* && \
    mkdir -p /mosquitto/config /mosquitto/data /mosquitto/log /mosquitto/certs /usr/lib/mosquitto /var/cache/mqtt

# Plugin built in stage 1.
COPY --from=plugin-builder /out/mapex-broker.so /usr/lib/mosquitto/mapex-broker.so

# Config template + entrypoint.
COPY config/mosquitto.conf.template /mosquitto/config/mosquitto.conf.template
COPY docker/entrypoint.sh /entrypoint.sh

# Filesystem ownership: Debian's mosquitto package creates the
# `mosquitto` system user. The entrypoint runs as that user so a
# compromised plugin never runs as root.
RUN chmod +x /entrypoint.sh && \
    chown -R mosquitto:mosquitto /mosquitto /var/cache/mqtt

# Healthcheck: TCP probe against the listener port. The broker requires
# auth (allow_anonymous=false), so an MQTT-level CONNECT probe would
# need credentials we don't want baked in. A pure TCP probe is enough:
# if the port is in LISTEN state, the broker is past plugin_init.
HEALTHCHECK --interval=15s --timeout=5s --start-period=10s --retries=3 \
    CMD nc -z 127.0.0.1 "${MQTT_LISTENER_PORT:-1883}" || exit 1

EXPOSE 1883 8883
VOLUME /mosquitto/data /mosquitto/log /mosquitto/certs /var/cache/mqtt

USER mosquitto

ENTRYPOINT ["/entrypoint.sh"]
