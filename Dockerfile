FROM golang:1.26-alpine AS builder

RUN apk add --no-cache gcc musl-dev

WORKDIR /build

COPY go.mod go.sum ./
RUN go mod download

COPY . .
ARG VERSION=dev
RUN CGO_ENABLED=1 GOOS=linux go build -trimpath -ldflags="-s -w -X main.version=${VERSION}" -o homelab-chatbot ./cmd/server

# ----

FROM alpine:3.21

RUN apk add --no-cache ca-certificates && \
    adduser -D -u 1000 appuser

WORKDIR /app
COPY --from=builder /build/homelab-chatbot .
COPY --from=ghcr.io/lobo235/homelab-mcp-server:latest /app/homelab-mcp-server /app/homelab-mcp-server

USER appuser

EXPOSE 8080

ENTRYPOINT ["/app/homelab-chatbot"]
