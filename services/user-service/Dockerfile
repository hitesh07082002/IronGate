FROM golang:1.25-alpine AS builder

WORKDIR /app

COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -o /out/user-service ./services/user-service

FROM alpine:3.20
RUN addgroup -S irongate && adduser -S -G irongate irongate

COPY --from=builder /out/user-service /usr/local/bin/user-service
USER irongate

EXPOSE 8081

CMD ["user-service"]
