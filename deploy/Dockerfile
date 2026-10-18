# Build from the repository root:  docker build -f deploy/Dockerfile .
# Multi-arch: docker buildx build --platform linux/amd64,linux/arm64 -f deploy/Dockerfile .
# The Go stage runs on the build machine and cross-compiles (no emulation);
# released images are published as ghcr.io/zadstack/gorun.
FROM --platform=$BUILDPLATFORM golang:1.27-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
ARG VERSION=dev
ARG TARGETOS
ARG TARGETARCH
RUN CGO_ENABLED=0 GOOS=${TARGETOS:-linux} GOARCH=${TARGETARCH} go build -trimpath -ldflags "-s -w -X main.version=${VERSION}" -o /out/gorun ./cmd/gorun \
 && mkdir -p /out/data

FROM gcr.io/distroless/static:nonroot
LABEL org.opencontainers.image.title="GoRun" \
      org.opencontainers.image.source="https://github.com/zadstack/gorun" \
      org.opencontainers.image.licenses="AGPL-3.0-only"
WORKDIR /app
COPY --from=build /out/gorun /app/gorun
COPY themes /app/themes
# Pre-create /data owned by the nonroot user so a fresh named volume inherits it.
COPY --from=build --chown=65532:65532 /out/data /data
ENV GORUN_ENV=prod \
    GORUN_DATA=/data \
    GORUN_ADDR=:8080
VOLUME /data
EXPOSE 8080
USER nonroot:nonroot
ENTRYPOINT ["/app/gorun","serve"]
