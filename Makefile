# dfm-mcp: the DFM check engine as a local MCP server. Run `make help` for the targets.

# A pseudo-version from master: v0.8.1 cannot read Go 1.27 export data. Pin the next tagged release when it ships.
STATICCHECK_VERSION := v0.7.0-0.dev.0.20261009230814-452d5bb86b45
GOSEC_VERSION    := 2.28.0

.PHONY: help build test vet lint ci run

help: ## List targets
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) | awk 'BEGIN {FS = ":.*?## "}; {printf "  %-16s %s\n", $$1, $$2}'

build: ## Build the binary into bin/
	@mkdir -p bin
	go build -o bin/dfm-mcp ./cmd/dfm-mcp

test: ## Run the tests
	go test ./...

vet: ## go vet
	go vet ./...

lint: vet ## staticcheck and gosec
	go run honnef.co/go/tools/cmd/staticcheck@$(STATICCHECK_VERSION) ./...
	go run github.com/securego/gosec/v2/cmd/gosec@v$(GOSEC_VERSION) -exclude-generated -quiet ./...

ci: test lint ## What CI runs

run: ## Run the server over stdio against a local API
	DFM_API_URL=http://localhost:8080 go run ./cmd/dfm-mcp
