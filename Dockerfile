# BUILDPLATFORM/TARGETOS/TARGETARCH are populated automatically by BuildKit.
# Declare defaults so plain `docker build` (BuildKit disabled, no buildx) does
# not expand them empty and feed `--platform=` / `GOOS= GOARCH=` downstream.
ARG BUILDPLATFORM=linux/amd64
FROM --platform=$BUILDPLATFORM golang:1.25 AS builder

ARG TARGETOS=linux
ARG TARGETARCH=amd64

WORKDIR /

COPY go.mod go.sum ./

RUN go mod download

COPY . .

# -B forces a rebuild: the `resource-state-metrics` target is file-based, not
# phony, so a binary copied in from the build context (no .dockerignore) would
# otherwise satisfy the timestamp check and ignore GOOS/GOARCH.
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH make -B resource-state-metrics

FROM ubuntu:24.04

RUN apt-get update && apt-get install -y ca-certificates && rm -rf /var/lib/apt/lists/*

RUN useradd -u 65534 -o -r nonroot

WORKDIR /

COPY --from=builder /resource-state-metrics .

EXPOSE 9998 9999

USER nonroot

ENTRYPOINT ["./resource-state-metrics"]
