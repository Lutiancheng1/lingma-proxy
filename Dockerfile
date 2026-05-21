# syntax=docker/dockerfile:1

FROM golang:1.23-alpine AS builder

WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags "-s -w" -o /out/lingma-proxy ./cmd/lingma-ipc-proxy

FROM alpine:3.21

RUN apk add --no-cache ca-certificates \
    && addgroup -S lingma \
    && adduser -S -G lingma lingma
COPY --from=builder /out/lingma-proxy /usr/local/bin/lingma-proxy

USER lingma
EXPOSE 8095
ENTRYPOINT ["lingma-proxy"]
CMD ["--host", "0.0.0.0", "--port", "8095"]
