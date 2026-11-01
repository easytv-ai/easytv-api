# syntax=docker/dockerfile:1

# ---- build stage -----------------------------------------------------------
FROM golang:1.27.3-alpine AS build
WORKDIR /src
RUN apk add --no-cache ca-certificates git
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod go mod download
COPY . .
ARG VERSION=dev
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 go build -trimpath -ldflags="-s -w -X easytv-api/common.Version=${VERSION}" -o /out/easytv-api .

# ---- runtime stage ---------------------------------------------------------
FROM alpine:3.24
LABEL org.opencontainers.image.title="easytv-api" \
      org.opencontainers.image.description="EasyTV backend: open-source AI short-drama studio" \
      org.opencontainers.image.source="https://github.com/easytv-ai/easytv-api" \
      org.opencontainers.image.licenses="AGPL-3.0-only"

# ffmpeg/ffprobe power the canvas media tools (trim, split, extract frames, compose)
RUN apk add --no-cache ca-certificates tzdata ffmpeg wget \
 && addgroup -S easytv && adduser -S -G easytv -u 10001 easytv

WORKDIR /app
COPY --from=build /out/easytv-api /app/easytv-api
RUN mkdir -p /app/upload /app/logs && chown -R easytv:easytv /app
USER easytv

ENV PORT=8383 \
    UPLOAD_DIR=/app/upload \
    GIN_MODE=release
EXPOSE 8383
VOLUME ["/app/upload", "/app/logs"]

HEALTHCHECK --interval=30s --timeout=3s --start-period=20s --retries=3 \
  CMD wget -qO- http://127.0.0.1:${PORT}/healthz >/dev/null || exit 1

ENTRYPOINT ["/app/easytv-api"]
CMD ["--log-dir", "/app/logs"]
