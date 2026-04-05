.PHONY: all clean lint build test test-race coverage run docker-up docker-down

GO_TEST_FLAGS ?=
COVERAGE_MIN ?= 70

all: build

clean:
	rm -rf bin coverage.out

lint:
	@test -z "$$(gofmt -l .)" || (gofmt -l . && exit 1)
	go vet ./...

build:
	mkdir -p bin
	go build -o bin/gateway ./cmd/gateway

test:
	go test ./... -v $(GO_TEST_FLAGS)

test-race:
	go test ./... -v -race $(GO_TEST_FLAGS)

coverage:
	go test ./... -covermode=atomic -coverprofile=coverage.out -coverpkg=./... $(GO_TEST_FLAGS)
	@set -eu; \
		cover_out="$$(go tool cover -func=coverage.out)"; \
		printf '%s\n' "$$cover_out"; \
		total=$$(printf '%s\n' "$$cover_out" | awk '/^total:/ {gsub("%","",$$3); print $$3}'); \
		awk "BEGIN { exit !($$total >= $(COVERAGE_MIN)) }" || (echo "coverage $$total% is below $(COVERAGE_MIN)%"; exit 1)

run:
	go run ./cmd/gateway

docker-up:
	docker-compose up --build

docker-down:
	docker-compose down
