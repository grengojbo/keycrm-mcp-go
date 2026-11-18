BINARY := bin/keycrm-mcp
GOVULNCHECK := golang.org/x/vuln/cmd/govulncheck@v1.10.0

.PHONY: build test check fmt lint vet generate security

build:
	CGO_ENABLED=0 go build -o $(BINARY) ./cmd/keycrm-mcp

test:
	go test -race ./...

check: fmt lint vet test

fmt:
	@test -z "$$(gofmt -l .)" || { echo "Не відформатовано gofmt:"; gofmt -l .; exit 1; }

lint:
	golangci-lint run ./...

vet:
	go vet ./...

# Поки що нічого не генерується; з етапу 1 тут з'явиться клієнт pkg/keycrm/api.
generate:
	go generate ./...

security:
	go run $(GOVULNCHECK) ./...
	gitleaks git --no-banner --redact .
