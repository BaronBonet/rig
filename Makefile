################################################################################
## Dependencies
################################################################################

.PHONY: dependencies-install
dependencies-install:
	@./scripts/dependencies/install.sh

################################################################################
## Generation
################################################################################

.PHONY: generate
generate:
	@./scripts/generate/go.sh

################################################################################
## Build
################################################################################

GIT_EXACT_TAG := $(shell git describe --tags --exact-match 2>/dev/null)
GIT_SHA := $(shell git rev-parse --short HEAD 2>/dev/null)
GIT_DIRTY := $(shell test -n "$$(git status --porcelain 2>/dev/null)" && echo -dirty)
BUILD_VERSION ?= $(if $(GIT_EXACT_TAG),$(GIT_EXACT_TAG),$(if $(GIT_SHA),dev-$(GIT_SHA)$(GIT_DIRTY),dev))
LDFLAGS := -X rig/internal/adapters/taskdaemon.currentFrontendBuildVersion=$(BUILD_VERSION)

.PHONY: build
build: generate
	@go build -ldflags "$(LDFLAGS)" -o ./local/bin/rig ./cmd/rig

################################################################################
## Test Lint
################################################################################

.PHONY: test
test:
	@go tool gotestsum --format pkgname --format-hide-empty-pkg -- ./...

.PHONY: format
format:
	@./scripts/ci/format.sh

.PHONY: lint-go
lint-go:
	@./scripts/ci/lint-go.sh

.PHONY: lint-all
lint-all: lint-go
