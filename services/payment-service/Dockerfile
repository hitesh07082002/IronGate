FROM golang:1.24-alpine AS builder

WORKDIR /app

COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -o /out/payment-service ./services/payment-service

FROM alpine:3.20

COPY --from=builder /out/payment-service /usr/local/bin/payment-service

EXPOSE 8083

CMD ["payment-service"]
