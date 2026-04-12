################################################################################
## Dependencies
################################################################################

.PHONY: dependencies-install
dependencies-install:
	@./scripts/dependencies/install.sh

.PHONY: dependencies-check
dependencies-check:
	@./scripts/dependencies/check.sh

################################################################################
## Generation
################################################################################

.PHONY: generate
generate: dependencies-check
	@./scripts/generate/go.sh

################################################################################
## Build
################################################################################

.PHONY: build
build: generate
	@go build -o ./local/bin/rig ./cmd/rig

################################################################################
## Test
################################################################################

.PHONY: test
test:
	@go tool gotestsum --format pkgname --format-hide-empty-pkg -- ./...

################################################################################
## Local Development
################################################################################

.PHONY: format
format:
	@./scripts/ci/format.sh

.PHONY: lint-go
lint-go: generate
	@./scripts/ci/lint-go.sh

.PHONY: lint-all
lint-all: lint-go
