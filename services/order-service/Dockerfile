FROM golang:1.24-alpine AS builder

WORKDIR /app

COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -o /out/order-service ./services/order-service

FROM alpine:3.20

COPY --from=builder /out/order-service /usr/local/bin/order-service

EXPOSE 8082

CMD ["order-service"]
