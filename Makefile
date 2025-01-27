BINARY_NAME := pachanger

LINTER := golangci-lint

.PHONY: all build test lint fmt ci

all: build test

build:
	@echo ">> Building $(BINARY_NAME)"
	mkdir -p bin
	go build -o bin/$(BINARY_NAME) .

test:
	@echo ">> Running tests"
	go test -v -race -cover -coverprofile=coverage.out ./...
	@echo ">> Test coverage in coverage.out"

lint:
	@echo ">> Running linter ($(LINTER))"
	$(LINTER) run

fmt:
	@echo ">> Formatting code"
	gofmt -w .
	goimports -w .

devdeps:
	@echo ">> Installing development dependencies"
	which goimports > /dev/null || go install golang.org/x/tools/cmd/goimports@latest
	which golangci-lint > /dev/null || go install github.com/golangci/golangci-lint/cmd/golangci-lint@latest

releasedeps:
	@echo ">> Installing release dependencies"
	which goreleaser > /dev/null || go install github.com/goreleaser/goreleaser/v2@latest

release_test: releasedeps
	@echo ">> Running release tests"
	goreleaser check

release: releasedeps
	@echo ">> Releasing"
	goreleaser release

ci: devdeps lint test
