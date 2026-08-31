.PHONY: build build-go build-rust build-web test test-unit test-bdd test-contracts test-integration test-e2e test-portable-conformance lint clean dev generate oss-release verify-oss-release bench-quick bench-standard bench-full bench-report bench-scale e2e-kind e2e-kind-teardown matrix matrix-report harness-build harness-smoke harness-stress harness-soak harness-pressure harness-chaos harness-redteam harness-latency harness-throughput harness-scale harness-inference harness-multimodel harness-fairness harness-chaos-recovery harness-all test-security test-pen test-soak-capability test-soak-ecosystem test-resilience test-production

# ──────────────────────────────────────────────
# Build
# ──────────────────────────────────────────────

build: build-go build-rust build-web

build-go:
	go build -o bin/fleet-controller ./cmd/fleet-controller
	go build -o bin/fleetctl ./cmd/fleetctl
	go build -o bin/grid-signals-publisher ./cmd/grid-signals-publisher

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
	go test -tags=contracts ./test/contracts/...

test-integration:
	go test -tags=integration -timeout=5m -count=1 ./test/integration/...

test-e2e:
	go test -tags=e2e -timeout=30m ./test/e2e/...

test-portable-conformance:
	bash hack/run-portable-conformance.sh

# ──────────────────────────────────────────────
# Lint
# ──────────────────────────────────────────────

lint: lint-go lint-rust lint-web

lint-go:
	golangci-lint run ./...

lint-rust:
	cargo clippy --workspace -- -D warnings
	cargo fmt --all -- --check

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
	docker build -t $(REGISTRY)/grid-signals-publisher:$(VERSION) -f deploy/docker/Dockerfile.grid-signals .

# ──────────────────────────────────────────────
# Portable OSS release
# ──────────────────────────────────────────────

oss-release:
	bash hack/build-oss-release.sh "$(VERSION)"

verify-oss-release:
	@archive=$$(bash hack/build-oss-release.sh "$(VERSION)"); \
		bash hack/verify-oss-release.sh "$$archive"

# ──────────────────────────────────────────────
# Benchmarks
# ──────────────────────────────────────────────

bench-quick:
	@echo "Running quick benchmarks (<5min)..."
	go test -bench=. -benchtime=10s ./pkg/routing/... ./pkg/modelpack/... ./pkg/placement/... ./pkg/controller/... ./pkg/store/... ./pkg/auth/... ./pkg/autoscaling/... ./pkg/observability/... ./pkg/kvcache/... ./pkg/ledger/... ./pkg/tenant/...

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

# ──────────────────────────────────────────────
# Test Harness
# ──────────────────────────────────────────────

HARNESS_URL     ?= http://localhost:8080
HARNESS_METRICS ?= http://localhost:9090
HARNESS_TOKEN   ?=
HARNESS_SECRET  ?=
HARNESS_OUTPUT  ?= test/harness/results/report.json
HARNESS_DURATION ?= 5m

HARNESS_FLAGS = --url=$(HARNESS_URL) --metrics=$(HARNESS_METRICS) --output=$(HARNESS_OUTPUT) --duration=$(HARNESS_DURATION)
ifdef HARNESS_TOKEN
HARNESS_FLAGS += --token=$(HARNESS_TOKEN)
endif
ifdef HARNESS_SECRET
HARNESS_FLAGS += --secret=$(HARNESS_SECRET)
endif

harness-build:
	go build -o bin/fleet-harness ./test/harness

harness-smoke: harness-build
	./bin/fleet-harness --suite=smoke $(HARNESS_FLAGS)

harness-stress: harness-build
	./bin/fleet-harness --suite=stress $(HARNESS_FLAGS)

harness-soak: harness-build
	./bin/fleet-harness --suite=soak $(HARNESS_FLAGS)

harness-pressure: harness-build
	./bin/fleet-harness --suite=pressure $(HARNESS_FLAGS)

harness-chaos: harness-build
	./bin/fleet-harness --suite=chaos $(HARNESS_FLAGS)

harness-redteam: harness-build
	./bin/fleet-harness --suite=redteam $(HARNESS_FLAGS)

harness-latency: harness-build
	./bin/fleet-harness --suite=latency $(HARNESS_FLAGS)

harness-throughput: harness-build
	./bin/fleet-harness --suite=throughput $(HARNESS_FLAGS)

harness-scale: harness-build
	./bin/fleet-harness --suite=scale $(HARNESS_FLAGS)

harness-inference: harness-build
	./bin/fleet-harness --suite=inference $(HARNESS_FLAGS)

harness-multimodel: harness-build
	./bin/fleet-harness --suite=multimodel $(HARNESS_FLAGS)

harness-fairness: harness-build
	./bin/fleet-harness --suite=fairness $(HARNESS_FLAGS)

harness-chaos-recovery: harness-build
	./bin/fleet-harness --suite=chaos-recovery $(HARNESS_FLAGS)

harness-all: harness-build
	./bin/fleet-harness --suite=all $(HARNESS_FLAGS)

# ──────────────────────────────────────────────
# Security & Penetration
# ──────────────────────────────────────────────

test-security:
	go test -tags=security ./test/security/...

test-pen:
	python3 test/security/pen_test.py --fleet-url=$(HARNESS_URL) --auth-secret=$(HARNESS_SECRET)

# ──────────────────────────────────────────────
# Soak & Resilience (Python)
# ──────────────────────────────────────────────

test-soak-capability:
	python3 test/soak/capability_soak.py --fleet-url=$(HARNESS_URL) --profile=quick --timeout=30

test-soak-ecosystem:
	python3 test/soak/ecosystem_soak.py --fleet-url=$(HARNESS_URL) --profile=quick --timeout=30

test-resilience:
	python3 test/soak/resilience_test.py --fleet-url=$(HARNESS_URL)

# ──────────────────────────────────────────────
# Production Test Suite
# ──────────────────────────────────────────────

test-production:
	bash test/production/run-all.sh --url=$(HARNESS_URL) --secret=$(HARNESS_SECRET)

e2e-kind:
	@echo "Setting up 3-cluster Kind environment..."
	bash test/e2e/kind/setup.sh
	@echo "Running verification..."
	bash test/e2e/kind/verify.sh

e2e-kind-teardown:
	bash test/e2e/kind/teardown.sh

bench-scale:
	@echo "Running fleet scale microbenchmarks..."
	go test -bench=BenchmarkInMemoryList -benchmem -benchtime=3s ./pkg/store/postgres/...
	go test -bench=BenchmarkSolve -benchmem -benchtime=3s ./pkg/placement/solver/...
	go test -bench=BenchmarkWeightedBalancer -benchmem -benchtime=3s ./pkg/routing/balancer/...
	go test -bench=BenchmarkReconcilePool -benchmem -benchtime=3s ./pkg/controller/...
