FROM golang:1.24-alpine AS builder

WORKDIR /app

COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -o /out/gateway ./cmd/gateway

FROM alpine:3.20

WORKDIR /app

COPY --from=builder /out/gateway /usr/local/bin/gateway
COPY configs/gateway.yaml /app/configs/gateway.yaml

EXPOSE 8080

CMD ["gateway", "-config", "/app/configs/gateway.yaml"]
