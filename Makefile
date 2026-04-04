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
## Build
################################################################################

.PHONY: build
build:
	@go build -o ./local/bin/agent ./cmd/agent

################################################################################
## Test
################################################################################

.PHONY: test
test:
	@go test ./...

################################################################################
## Local Development
################################################################################

.PHONY: format
format:
	@./scripts/ci/format.sh

.PHONY: lint-go
lint-go:
	@./scripts/ci/lint-go.sh

.PHONY: lint-all
lint-all: lint-go
