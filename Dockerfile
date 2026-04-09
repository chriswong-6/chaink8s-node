FROM golang:1.25.4 AS builder
WORKDIR /workspace
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -o /out/akash        ./cmd/akash
RUN CGO_ENABLED=0 GOOS=linux go build -o /out/ck8s-monitor ./cmd/ck8s-monitor
RUN CGO_ENABLED=0 GOOS=linux go build -o /out/ck8s-query   ./cmd/ck8s-query

FROM ubuntu:noble
RUN apt-get update && apt-get install -y ca-certificates && rm -rf /var/lib/apt/lists/*
COPY --from=builder /out/akash        /usr/local/bin/akash
COPY --from=builder /out/ck8s-monitor /usr/local/bin/ck8s-monitor
COPY --from=builder /out/ck8s-query   /usr/local/bin/ck8s-query
