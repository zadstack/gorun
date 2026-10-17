# Build from the repository root:  docker build -f deploy/Dockerfile .
FROM golang:1.27-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
ARG VERSION=dev
RUN CGO_ENABLED=0 go build -trimpath -ldflags "-s -w -X main.version=${VERSION}" -o /out/gorun ./cmd/gorun \
 && mkdir -p /out/data

FROM gcr.io/distroless/static:nonroot
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
