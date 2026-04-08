FROM golang:1.25-alpine AS builder

WORKDIR /app

ARG GOPROXY=https://proxy.golang.org,direct
ARG GOSUMDB=sum.golang.org
ENV GOPROXY=${GOPROXY}
ENV GOSUMDB=${GOSUMDB}

COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -o /out/order-service ./services/order-service

FROM alpine:3.20
RUN addgroup -S irongate && adduser -S -G irongate irongate

COPY --from=builder /out/order-service /usr/local/bin/order-service
USER irongate

EXPOSE 8082

CMD ["order-service"]
