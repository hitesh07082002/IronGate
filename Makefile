.PHONY: all clean lint build test test-race coverage run docker-up docker-down load-test benchmark benchmark-scenario benchmark-render benchmark-test bootstrap-production deploy-production check-production observatory-up observatory-down observatory-logs observatory-reset

GO_TEST_FLAGS ?=
COVERAGE_MIN ?= 70
DOCKER_COMPOSE ?= $(shell if docker compose version >/dev/null 2>&1; then printf '%s' 'docker compose'; elif command -v docker-compose >/dev/null 2>&1; then printf '%s' 'docker-compose'; fi)
K6_CMD ?= $(shell if command -v k6 >/dev/null 2>&1; then printf '%s' 'k6'; elif command -v mise >/dev/null 2>&1 && mise exec -- k6 version >/dev/null 2>&1; then printf '%s' 'mise exec -- k6'; fi)

all: build

clean:
	rm -rf bin coverage.out

lint:
	@test -z "$$(gofmt -l .)" || (gofmt -l . && exit 1)
	go vet ./...

build:
	mkdir -p bin
	go build -o bin/gateway ./cmd/gateway
	go build -o bin/observatory ./cmd/observatory

test:
	go test ./... -v $(GO_TEST_FLAGS)

test-race:
	go test ./... -v -race $(GO_TEST_FLAGS)

coverage:
	go test ./... -covermode=atomic -coverprofile=coverage.out $(GO_TEST_FLAGS)
	@set -eu; \
		cover_out="$$(go tool cover -func=coverage.out)"; \
		printf '%s\n' "$$cover_out"; \
		total=$$(printf '%s\n' "$$cover_out" | awk '/^total:/ {gsub("%","",$$3); print $$3}'); \
		awk "BEGIN { exit !($$total >= $(COVERAGE_MIN)) }" || (echo "coverage $$total% is below $(COVERAGE_MIN)%"; exit 1)

run:
	REDIS_ADDR=$${REDIS_ADDR:-127.0.0.1:6379} go run ./cmd/gateway

docker-up:
	@test -n "$(DOCKER_COMPOSE)" || (echo "Docker Compose is required"; exit 1)
	$(DOCKER_COMPOSE) up --build

docker-down:
	@test -n "$(DOCKER_COMPOSE)" || (echo "Docker Compose is required"; exit 1)
	$(DOCKER_COMPOSE) down

observatory-up: ## Start full observatory stack (Tempo + OTel Collector + gateway)
	@command -v docker >/dev/null 2>&1 || (echo "Docker is required for observatory-up"; exit 1)
	@test -n "$(DOCKER_COMPOSE)" || (echo "Docker Compose is required"; exit 1)
	@test -n "$${JWT_SECRET:-}" || (echo "JWT_SECRET must be set"; exit 1)
	@test -n "$${GRAFANA_ADMIN_USER:-}" || (echo "GRAFANA_ADMIN_USER must be set"; exit 1)
	@test -n "$${GRAFANA_ADMIN_PASSWORD:-}" || (echo "GRAFANA_ADMIN_PASSWORD must be set"; exit 1)
	@test -n "$${ADMIN_TOKEN:-}" || (echo "ADMIN_TOKEN must be set"; exit 1)
	@test -n "$${DEMO_TOKEN:-}" || (echo "DEMO_TOKEN must be set"; exit 1)
	docker pull grafana/k6:0.51.0
	$(DOCKER_COMPOSE) -f docker-compose.yml -f docker-compose.observatory.yml up -d --build

observatory-down: ## Stop observatory stack
	@test -n "$(DOCKER_COMPOSE)" || (echo "Docker Compose is required"; exit 1)
	@test -n "$${JWT_SECRET:-}" || (echo "JWT_SECRET must be set"; exit 1)
	@test -n "$${GRAFANA_ADMIN_USER:-}" || (echo "GRAFANA_ADMIN_USER must be set"; exit 1)
	@test -n "$${GRAFANA_ADMIN_PASSWORD:-}" || (echo "GRAFANA_ADMIN_PASSWORD must be set"; exit 1)
	@test -n "$${ADMIN_TOKEN:-}" || (echo "ADMIN_TOKEN must be set"; exit 1)
	@test -n "$${DEMO_TOKEN:-}" || (echo "DEMO_TOKEN must be set"; exit 1)
	$(DOCKER_COMPOSE) -f docker-compose.yml -f docker-compose.observatory.yml down

observatory-logs: ## Tail observatory container logs
	@test -n "$(DOCKER_COMPOSE)" || (echo "Docker Compose is required"; exit 1)
	$(DOCKER_COMPOSE) -f docker-compose.yml -f docker-compose.observatory.yml logs -f

observatory-reset: ## Reset observatory state through the public API
	@command -v curl >/dev/null 2>&1 || (echo "curl is required for observatory-reset"; exit 1)
	@test -n "$${DEMO_TOKEN:-}" || (echo "DEMO_TOKEN must be set"; exit 1)
	curl -fsS -XPOST -H "Authorization: Bearer $$DEMO_TOKEN" http://127.0.0.1:9000/api/reset

load-test:
	@test -n "$(K6_CMD)" || (echo "k6 is required for load-test; run 'mise install' in the repo root first"; exit 1)
	@command -v curl >/dev/null 2>&1 || (echo "curl is required for load-test"; exit 1)
	@set -eu; \
		base_url="$${IRONGATE_BASE_URL:-http://127.0.0.1:8080}"; \
		ready_url="$${base_url%/}/ready"; \
		if ! curl --connect-timeout 2 --max-time 5 -fsS "$$ready_url" >/dev/null 2>&1; then \
			echo "gateway is not reachable at $$base_url"; \
			echo "start the stack first with ./demo.sh --keep-stack or docker compose up -d --build"; \
			exit 1; \
		fi; \
		IRONGATE_BASE_URL="$$base_url" $(K6_CMD) run benchmarks/smoke.js

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

bootstrap-production:
	./scripts/bootstrap-production-host.sh

deploy-production:
	./scripts/deploy-production.sh

check-production:
	./scripts/check-production-health.sh
