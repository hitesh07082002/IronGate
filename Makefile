.PHONY: all clean lint build test test-race coverage run docker-up docker-down load-test benchmark benchmark-scenario benchmark-render benchmark-test

GO_TEST_FLAGS ?=
COVERAGE_MIN ?= 70
DOCKER_COMPOSE ?= $(shell if docker compose version >/dev/null 2>&1; then printf '%s' 'docker compose'; elif command -v docker-compose >/dev/null 2>&1; then printf '%s' 'docker-compose'; fi)

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
	@test -n "$(DOCKER_COMPOSE)" || (echo "Docker Compose is required"; exit 1)
	$(DOCKER_COMPOSE) up --build

docker-down:
	@test -n "$(DOCKER_COMPOSE)" || (echo "Docker Compose is required"; exit 1)
	$(DOCKER_COMPOSE) down

load-test:
	@command -v k6 >/dev/null 2>&1 || (echo "k6 is required for load-test"; exit 1)
	IRONGATE_BASE_URL="$${IRONGATE_BASE_URL:-http://127.0.0.1:8080}" k6 run benchmarks/smoke.js

benchmark:
	@command -v python3 >/dev/null 2>&1 || (echo "python3 is required for benchmark"; exit 1)
	python3 benchmarks/runner.py run --scenario all

benchmark-test:
	@command -v python3 >/dev/null 2>&1 || (echo "python3 is required for benchmark-test"; exit 1)
	python3 -m unittest discover -s benchmarks -p 'test_*.py' -v

benchmark-scenario:
	@command -v python3 >/dev/null 2>&1 || (echo "python3 is required for benchmark-scenario"; exit 1)
	@test -n "$${SCENARIO:-}" || (echo "SCENARIO is required"; exit 1)
	python3 benchmarks/runner.py run --scenario "$${SCENARIO}"

benchmark-render:
	@command -v python3 >/dev/null 2>&1 || (echo "python3 is required for benchmark-render"; exit 1)
	@test -n "$${RESULT_DIR:-}" || (echo "RESULT_DIR is required"; exit 1)
	python3 benchmarks/runner.py render --result-dir "$${RESULT_DIR}"
