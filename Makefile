# maas-client-go developer tasks.
#
# `make verify` is the gate used by CI and by reviewers before merging.

GO ?= go
CGO_ENABLED ?= 0

.PHONY: help
help: ## Show available targets
	@grep -hE '^[a-zA-Z_-]+:.*?## ' $(MAKEFILE_LIST) | awk 'BEGIN {FS = ":.*?## "}; {printf "  \033[36m%-14s\033[0m %s\n", $$1, $$2}'

.PHONY: build
build: ## Compile all packages
	$(GO) build ./...

.PHONY: test
test: ## Run tests with the race detector
	$(GO) test -race ./...

.PHONY: cover
cover: ## Run tests and print coverage
	$(GO) test -cover ./...

.PHONY: fmt
fmt: ## Format all Go files
	$(GO) fmt ./...

.PHONY: fmt-check
fmt-check: ## Fail if any file is not gofmt-ed
	@out="$$(gofmt -l $$(find . -name '*.go' -not -path './.g*' -not -path './.idea/*'))"; \
	if [ -n "$$out" ]; then echo "not gofmt-ed:"; echo "$$out"; exit 1; fi

.PHONY: vet
vet: ## Run go vet
	$(GO) vet ./...

.PHONY: lint
lint: ## Run golangci-lint if installed
	@command -v golangci-lint >/dev/null 2>&1 && golangci-lint run || echo "golangci-lint not installed; skipping"

.PHONY: check-english
check-english: ## Fail if any Go file contains CJK characters (comments and messages are English-only)
	@out="$$(grep -rln '[一-鿿]' --include='*.go' \
		--exclude-dir=.gocache --exclude-dir=.gomodcache --exclude-dir=.gopath \
		--exclude-dir=.gotmp --exclude-dir=.idea . 2>/dev/null || true)"; \
	if [ -n "$$out" ]; then echo "CJK characters found in:"; echo "$$out"; exit 1; fi

.PHONY: check-env
check-env: ## Fail if library code reads environment variables
	@if grep -rn 'os\.Getenv\|os\.LookupEnv' --include='*.go' \
		--exclude-dir=.gocache --exclude-dir=.gomodcache --exclude-dir=.gopath \
		--exclude-dir=.gotmp --exclude-dir=.idea . 2>/dev/null | grep -v '_test.go'; then \
		echo "library code must not read environment variables"; exit 1; fi

.PHONY: check-sensitive
check-sensitive: ## Fail if sources contain deployment-specific or sensitive content
	@./scripts/check-sensitive.sh

.PHONY: check-secrets
check-secrets: ## Scan for committed secrets if gitleaks is installed
	@command -v gitleaks >/dev/null 2>&1 && gitleaks detect --no-git -v || echo "gitleaks not installed; skipping"

.PHONY: verify
verify: build vet fmt-check test check-english check-env check-sensitive ## Full local gate
	@echo "verify: OK"
