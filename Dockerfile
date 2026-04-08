FROM golang:1.25-alpine AS builder

WORKDIR /app

ARG GOPROXY=https://proxy.golang.org,direct
ARG GOSUMDB=sum.golang.org
ENV GOPROXY=${GOPROXY}
ENV GOSUMDB=${GOSUMDB}

COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -o /out/gateway ./cmd/gateway

FROM alpine:3.20

WORKDIR /app
RUN addgroup -S irongate && adduser -S -G irongate irongate

COPY --from=builder /out/gateway /usr/local/bin/gateway
COPY configs/gateway.yaml /app/configs/gateway.yaml
USER irongate

EXPOSE 8080

CMD ["gateway"]
