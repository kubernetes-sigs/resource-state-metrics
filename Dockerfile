FROM --platform=$BUILDPLATFORM golang:1.25 AS builder

ARG TARGETOS
ARG TARGETARCH

WORKDIR /

COPY go.mod go.sum ./

RUN go mod download

COPY . .

RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH make resource-state-metrics

FROM ubuntu:24.04

RUN apt-get update && apt-get install -y ca-certificates && rm -rf /var/lib/apt/lists/*

RUN useradd -u 65534 -o -r nonroot

WORKDIR /

COPY --from=builder /resource-state-metrics .

EXPOSE 9998 9999

USER nonroot

ENTRYPOINT ["./resource-state-metrics"]
