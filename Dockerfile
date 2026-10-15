# syntax=docker/dockerfile:1

ARG GO_VERSION=1.27.2
ARG DEBIAN_VERSION=bookworm

# --- Сборка бинарников Go ---
FROM golang:${GO_VERSION}-${DEBIAN_VERSION} AS build
WORKDIR /src

COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod go mod download

COPY . .
ARG VERSION=dev
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 go build -trimpath \
      -ldflags "-s -w -X github.com/zubans/agy-pool/internal/version.Version=${VERSION}" \
      -o /out/ ./cmd/agy-pool ./cmd/sandboxd

# --- Загрузка Antigravity CLI из релизов GitHub с проверкой контрольной суммы ---
FROM debian:${DEBIAN_VERSION}-slim AS agy
ARG TARGETARCH
ARG AGY_VERSION=1.3.6
ARG AGY_SHA256_AMD64=e88ed835cbc4af504f64dfe20175f5572dc2a425dbfa2fcf55c0358bb444d80c
ARG AGY_SHA256_ARM64=0e11472f376e2943c6f812280dea49b204acc3be15794c6604a46972c5995b12
RUN apt-get update \
 && apt-get install -y --no-install-recommends ca-certificates curl \
 && rm -rf /var/lib/apt/lists/*
RUN set -eu; \
    case "${TARGETARCH:-amd64}" in \
      amd64) asset=agy_cli_linux_x64.tar.gz;   sum="${AGY_SHA256_AMD64}" ;; \
      arm64) asset=agy_cli_linux_arm64.tar.gz; sum="${AGY_SHA256_ARM64}" ;; \
      *) echo "unsupported architecture: ${TARGETARCH}" >&2; exit 1 ;; \
    esac; \
    curl -fsSL -o /tmp/agy.tar.gz \
      "https://github.com/google-antigravity/antigravity-cli/releases/download/${AGY_VERSION}/${asset}"; \
    echo "${sum}  /tmp/agy.tar.gz" | sha256sum -c -; \
    mkdir -p /out; \
    tar -xzf /tmp/agy.tar.gz -C /out antigravity; \
    chmod 0755 /out/antigravity

# --- Исполнитель песочниц (пока заглушка) ---
FROM gcr.io/distroless/static-debian12:nonroot AS sandboxd
COPY --from=build /out/sandboxd /sandboxd
EXPOSE 8090
HEALTHCHECK --interval=10s --timeout=5s --start-period=5s --retries=3 CMD ["/sandboxd", "healthcheck"]
ENTRYPOINT ["/sandboxd"]

# --- Приложение: сервер agy-pool и Antigravity CLI ---
FROM debian:${DEBIAN_VERSION}-slim AS app

# tzdata — для AGY_TIMEZONE (time.LoadLocation), ca-certificates — для HTTPS к провайдерам.
RUN apt-get update \
 && apt-get install -y --no-install-recommends ca-certificates tzdata \
 && rm -rf /var/lib/apt/lists/*

COPY --from=agy /out/antigravity /usr/local/bin/antigravity
RUN ln -s /usr/local/bin/antigravity /usr/local/bin/agy
COPY --from=build /out/agy-pool /usr/local/bin/agy-pool

# Каталоги томов создаются заранее с владельцем agy: Docker переносит владельца в новые тома.
RUN groupadd --system --gid 10001 agy \
 && useradd --system --uid 10001 --gid agy --home-dir /home/agy --create-home --shell /usr/sbin/nologin agy \
 && mkdir -p /data/accounts /data/skills /data/work \
 && chown -R agy:agy /data

USER agy
WORKDIR /data/work
ENV AGY_DATA_DIR=/data
EXPOSE 8080
HEALTHCHECK --interval=10s --timeout=5s --start-period=40s --retries=3 CMD ["agy-pool", "healthcheck"]
ENTRYPOINT ["agy-pool"]
CMD ["serve"]
