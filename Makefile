.PHONY: lint build test run docker-up docker-down

lint:
	@test -z "$$(gofmt -l .)" || (gofmt -l . && exit 1)
	go vet ./...

build:
	go build -o bin/gateway ./cmd/gateway

test:
	go test ./... -v

run:
	go run ./cmd/gateway

docker-up:
	docker-compose up --build

docker-down:
	docker-compose down
