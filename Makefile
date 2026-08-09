.PHONY: all test web build docker smoke clean

VERSION ?= 0.1.0-dev
GIT_COMMIT ?= $(shell git rev-parse --short HEAD 2>/dev/null || printf unknown)
BUILD_DATE ?= $(shell date -u +%Y-%m-%dT%H:%M:%SZ)
LDFLAGS = -X github.com/hkjang/relio/internal/platform/version.Version=$(VERSION) -X github.com/hkjang/relio/internal/platform/version.GitCommit=$(GIT_COMMIT) -X github.com/hkjang/relio/internal/platform/version.BuildDate=$(BUILD_DATE)

all: test build

web:
	cd web && npm ci && npm run build

test:
	go test ./...
	go vet ./...
	cd web && npm ci && npm run typecheck

build: web
	go build -trimpath -ldflags="$(LDFLAGS)" -o dist/relio ./cmd/relio

docker:
	docker build --build-arg VERSION=$(VERSION) --build-arg GIT_COMMIT=$(GIT_COMMIT) --build-arg BUILD_DATE=$(BUILD_DATE) -t relio:$(VERSION) .

smoke:
	./scripts/offline-smoke.sh http://127.0.0.1:8080 admin ChangeMe-Relio-2026

clean:
	go clean
	cd web && rm -rf node_modules
