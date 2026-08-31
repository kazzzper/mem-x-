# ---- build stage ----
FROM golang:1.27-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download 2>/dev/null || true
COPY . .
# Build all three binaries: server, CLI, and URL builder.
# CGO_ENABLED=0 for static, fully self-contained binaries.
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/mem-x     ./cmd/mem-x && \
    CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/memx-cli ./cmd/memx-cli && \
    CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/memx-url ./cmd/memx-url

# ---- runtime stage ----
FROM alpine:3.20
RUN apk add --no-cache ca-certificates tzdata
COPY --from=build /out/mem-x     /usr/local/bin/mem-x
COPY --from=build /out/memx-cli /usr/local/bin/memx-cli
COPY --from=build /out/memx-url /usr/local/bin/memx-url

# Data directory for AOF persistence.
RUN mkdir -p /data && addgroup -S memx && adduser -S memx -G memx
USER memx
VOLUME ["/data"]
EXPOSE 6379 6380

# Environment variables (all optional, see env-var helpers in main.go):
#   MEMX_ADDR / MEMX_PORT   listen address
#   MEMX_PASSWORD           requirepass (AUTH)
#   MEMX_TLS_CERT / MEMX_TLS_KEY
#   MEMX_AOF / MEMX_APPENDFSYNC
#   MEMX_LOG_LEVEL          (default: info)
#   MEMX_SHARDS / MEMX_MAX_CONN / ...
#
# Build the connection URL:
#   docker exec <container> memx-url
#   MEMX_PASSWORD=secret docker compose run --rm memx memx-url

ENTRYPOINT ["mem-x"]
CMD ["-addr", ":6379"]