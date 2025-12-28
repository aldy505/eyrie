FROM golang:1.25.5-trixie@sha256:8e8f9c84609b6005af0a4a8227cee53d6226aab1c6dcb22daf5aeeb8b05480e1 AS builder

WORKDIR /usr/src/eyrie

RUN apt-get update && apt-get install -y libssl-dev git

COPY . .

RUN CGO_ENABLED=1 go build -o /usr/local/bin/eyrie \
    -ldflags="-s -w -X 'main.Version=$(git describe --tags --always --dirty)'" \
    .

FROM debian:trixie-20251208-slim@sha256:e711a7b30ec1261130d0a121050b4ed81d7fb28aeabcf4ea0c7876d4e9f5aca2 AS runtime

WORKDIR /etc/eyrie

RUN apt-get update && \
    apt-get install -y ca-certificates libssl-dev && \
    rm -rf /var/lib/apt/lists/* && \
    mkdir -p /var/lib/eyrie

COPY LICENSE README.md /etc/eyrie/

COPY --from=builder /usr/local/bin/eyrie /usr/local/bin/eyrie

ENTRYPOINT ["/usr/local/bin/eyrie"]