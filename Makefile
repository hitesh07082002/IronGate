.PHONY: lint build test test-race run docker-up docker-down

GO_TEST_FLAGS ?=

lint:
	@test -z "$$(gofmt -l .)" || (gofmt -l . && exit 1)
	go vet ./...

build:
	go build -o bin/gateway ./cmd/gateway

test:
	go test ./... -v $(GO_TEST_FLAGS)

test-race:
	go test ./... -v -race

run:
	go run ./cmd/gateway

docker-up:
	docker-compose up --build

docker-down:
	docker-compose down
