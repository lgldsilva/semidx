GOTOOLCHAIN := go1.25.12
export GOTOOLCHAIN

.PHONY: all build test bench lint fmt gosec vulncheck docker-build clean help

all: build test lint

build:
	go build ./...

test:
	go test -race -shuffle=on -count=1 ./...

test-short:
	go test -race -shuffle=on -short -count=1 ./...

bench:
	go test -bench=. -benchmem -benchtime=1s ./...

lint:
	go run github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.12.2 run ./...

fmt:
	gofmt -l . | tee /dev/stderr | test ! -s /dev/stdin

fmt-fix:
	gofmt -w .

gosec:
	go run github.com/securego/gosec/v2/cmd/gosec@v2.27.1 -quiet ./...

vulncheck:
	go run golang.org/x/vuln/cmd/govulncheck@latest ./...

docker-build:
	docker build -t semidx:latest .

clean:
	go clean -cache -testcache
	rm -f semidx

help:
	@echo "Usage: make [target]"
	@echo ""
	@echo "Primary targets:"
	@echo "  all          Build, test, and lint (default)"
	@echo "  build        Compile all packages"
	@echo "  test         Run all tests with race detector"
	@echo "  test-short   Run tests in short mode"
	@echo "  bench        Run benchmarks"
	@echo ""
	@echo "Quality gates:"
	@echo "  fmt          Check formatting (non-zero exit if unformatted)"
	@echo "  fmt-fix      Apply gofmt fixes"
	@echo "  lint         Run golangci-lint"
	@echo "  gosec        Run gosec security scanner"
	@echo "  vulncheck    Run govulncheck"
	@echo ""
	@echo "Docker:"
	@echo "  docker-build Build Docker image"
	@echo ""
	@echo "Utilities:"
	@echo "  clean        Remove build/test artifacts"
