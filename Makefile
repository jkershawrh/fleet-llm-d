.PHONY: build build-go build-rust build-web test test-unit test-bdd test-contracts test-e2e lint clean dev generate bench-quick bench-standard bench-full bench-report matrix matrix-report

# ──────────────────────────────────────────────
# Build
# ──────────────────────────────────────────────

build: build-go build-rust build-web

build-go:
	go build -o bin/fleet-controller ./cmd/fleet-controller
	go build -o bin/fleetctl ./cmd/fleetctl

build-rust:
	cargo build --workspace --release

build-web:
	cd web && npm run build

# ──────────────────────────────────────────────
# Test
# ──────────────────────────────────────────────

test: test-unit test-bdd test-contracts

test-unit: test-unit-go test-unit-rust

test-unit-go:
	go test -race -count=1 ./pkg/...

test-unit-rust:
	cargo test --workspace

test-bdd:
	go test -tags=bdd ./test/bdd/...

test-contracts:
	go test ./test/contracts/...

test-e2e:
	go test -tags=e2e -timeout=30m ./test/e2e/...

# ──────────────────────────────────────────────
# Lint
# ──────────────────────────────────────────────

lint: lint-go lint-rust lint-web

lint-go:
	golangci-lint run ./...

lint-rust:
	cargo clippy --workspace -- -D warnings
	cargo fmt --workspace -- --check

lint-web:
	cd web && npm run lint

# ──────────────────────────────────────────────
# Code Generation
# ──────────────────────────────────────────────

generate:
	./hack/generate.sh

generate-proto:
	@echo "Generating protobuf Go code..."
	@for dir in api/proto/*/v1; do \
		protoc --go_out=. --go_opt=paths=source_relative \
			--go-grpc_out=. --go-grpc_opt=paths=source_relative \
			$$dir/*.proto; \
	done

generate-crds:
	@echo "CRD schemas are hand-maintained in api/crds/"

# ──────────────────────────────────────────────
# Development
# ──────────────────────────────────────────────

dev:
	docker compose up -d

dev-down:
	docker compose down -v

clean:
	rm -rf bin/ target/ web/.next/ web/out/
	go clean -cache -testcache

# ──────────────────────────────────────────────
# Docker
# ──────────────────────────────────────────────

REGISTRY ?= ghcr.io/llm-d
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo "dev")

docker-build:
	docker build -t $(REGISTRY)/fleet-controller:$(VERSION) -f deploy/docker/Dockerfile.controller .
	docker build -t $(REGISTRY)/fleet-agent:$(VERSION) -f deploy/docker/Dockerfile.agent .
	docker build -t $(REGISTRY)/fleet-gateway:$(VERSION) -f deploy/docker/Dockerfile.gateway .

# ──────────────────────────────────────────────
# Benchmarks
# ──────────────────────────────────────────────

bench-quick:
	@echo "Running quick benchmarks (<5min)..."
	go test -bench=. -benchtime=10s ./pkg/routing/... ./pkg/modelpack/...

bench-standard:
	@echo "Running standard benchmarks (<30min)..."
	go test -bench=. -benchtime=30s ./pkg/...

bench-full:
	@echo "Running full benchmark suite (<2hr)..."
	go test -bench=. -benchtime=60s -count=3 ./pkg/...
	cargo bench --workspace

bench-report:
	@echo "Generating benchmark report..."
	@mkdir -p test/benchmarks/reports
	go test -bench=. -benchtime=10s -json ./pkg/... > test/benchmarks/reports/go-bench.json 2>&1 || true
	@echo "Report written to test/benchmarks/reports/"

# ──────────────────────────────────────────────
# Test Matrix
# ──────────────────────────────────────────────

matrix:
	@echo "Generating test matrix..."
	@cat test/matrix/matrix.yaml

matrix-report:
	@echo "Matrix report generation requires the matrix-reporter tool"
	@echo "Install: go install github.com/llm-d/fleet-llm-d/cmd/matrix-reporter@latest"
