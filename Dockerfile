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

RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH make resource-state-metrics

FROM gcr.io/distroless/static-debian12:latest@sha256:932f28cabd51c9f9e1e25d3b9d1f09119036722ce86f5c5bd723c4f51cc2d6dc

COPY --from=builder /resource-state-metrics /

USER nonroot

ENTRYPOINT ["/resource-state-metrics"]

EXPOSE 9998 9999
