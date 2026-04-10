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
	@rm -rf ./internal/adapters/repository/sqlite/generated
	@go tool sqlc generate -f ./internal/adapters/repository/sqlite/sqlc.yaml
	@go tool mockery --config=.mockery.yaml

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
test: generate
	@go test ./...

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
