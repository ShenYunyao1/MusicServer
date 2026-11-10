ARG GO_IMAGE=golang:1.26-bookworm
ARG RUNTIME_IMAGE=debian:bookworm-slim
FROM ${GO_IMAGE} AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod go mod download
COPY cmd ./cmd
COPY internal ./internal
ARG VCS_REF=unknown
ARG VCS_MODIFIED=true
RUN --mount=type=cache,target=/go/pkg/mod --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 go build -trimpath -buildvcs=false \
    -ldflags="-X pikmelo-server/internal/version.buildCommit=${VCS_REF} -X pikmelo-server/internal/version.buildModified=${VCS_MODIFIED}" \
    -o /out/pikmelo-server ./cmd/pikmelo-server

FROM ${RUNTIME_IMAGE} AS runtime
RUN apt-get update \
    && apt-get install -y --no-install-recommends ca-certificates ffmpeg tar \
    && rm -rf /var/lib/apt/lists/* \
    && groupadd --gid 10001 pikmelo \
    && useradd --uid 10001 --gid 10001 --no-create-home --home-dir /data --shell /usr/sbin/nologin pikmelo \
    && mkdir -p /data \
    && chown 10001:10001 /data
COPY --from=build /out/pikmelo-server /usr/local/bin/pikmelo-server
ARG VCS_REF=unknown
ARG IMAGE_VERSION=local
LABEL org.opencontainers.image.title="Pikmelo Server" \
      org.opencontainers.image.description="拾律服务器" \
      org.opencontainers.image.source="https://github.com/ShenYunyao1/Pikmelo-Server" \
      org.opencontainers.image.revision="${VCS_REF}" \
      org.opencontainers.image.version="${IMAGE_VERSION}"
ENV PIKMELO_DEPLOYMENT=docker
USER 10001:10001
WORKDIR /data
EXPOSE 8080
STOPSIGNAL SIGTERM
HEALTHCHECK --interval=15s --timeout=5s --start-period=30s --retries=3 \
    CMD ["/usr/local/bin/pikmelo-server", "healthcheck"]
ENTRYPOINT ["/usr/local/bin/pikmelo-server"]
CMD ["-addr", "0.0.0.0:8080", "-data", "/data/data/library.sqlite", "-media-root", "/data/media"]
