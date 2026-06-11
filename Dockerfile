FROM node:24.12.0-trixie@sha256:9fabb41bc32c72b02fd332bb6b6a17e01117d7eaa379a497a5adf7e1651baa2b AS frontend

WORKDIR /usr/src/eyrie

COPY frontend/ .

RUN npm ci --loglevel=http && \
    npm run build && \
    rm -rf node_modules

FROM golang:1.26.3-trixie@sha256:d08bf3ed2bd263088ca8e23fefaf10f1b71769f6932f0a4017ba28d2a5baf001 AS backend

WORKDIR /usr/src/eyrie

RUN apt-get update && apt-get install -y libssl-dev git

COPY . .

COPY --from=frontend /usr/src/eyrie/dist ./frontend/dist

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

COPY --from=backend /usr/local/bin/eyrie /usr/local/bin/eyrie

ENTRYPOINT ["/usr/local/bin/eyrie"]