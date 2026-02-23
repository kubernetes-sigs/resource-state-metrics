FROM golang:1.26 AS builder

WORKDIR /

COPY go.mod go.sum ./

RUN go mod download

COPY . .

RUN make resource-state-metrics

FROM ubuntu:24.04

RUN apt-get update && apt-get install -y ca-certificates && rm -rf /var/lib/apt/lists/*

RUN useradd -u 65534 -r nonroot

WORKDIR /

COPY --from=builder /resource-state-metrics .

EXPOSE 9998 9999

USER nonroot

CMD ["./resource-state-metrics"]
