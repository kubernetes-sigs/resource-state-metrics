# check=skip=RedundantTargetPlatform

# BUILDPLATFORM/TARGETPLATFORM/TARGETOS/TARGETARCH are populated automatically
# by BuildKit. Don't give the ARGs defaults: a default takes precedence over the
# value BuildKit provides. The fallbacks where they are used only apply when
# they are empty, e.g. with the legacy builder (BuildKit disabled), which then
# builds linux/amd64 for both the binary and the base image.
ARG BUILDPLATFORM
FROM --platform=${BUILDPLATFORM:-linux/amd64} golang:1.25 AS builder

ARG TARGETOS
ARG TARGETARCH

WORKDIR /

COPY go.mod go.sum ./

RUN go mod download

COPY . .

RUN CGO_ENABLED=0 GOOS=${TARGETOS:-linux} GOARCH=${TARGETARCH:-amd64} make resource-state-metrics

FROM --platform=${TARGETPLATFORM:-linux/amd64} gcr.io/distroless/static-debian12:latest@sha256:d75cdd72874d4790092fcb1b058493ecf6bb5bf2b2b897045b00ff01d91843f2

COPY --from=builder /resource-state-metrics /

USER nonroot

ENTRYPOINT ["/resource-state-metrics"]

EXPOSE 9998 9999
