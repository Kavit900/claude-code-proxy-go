BINARY=claude-code-proxy-go
CMD=./cmd/server

.PHONY: build run dev deps tidy lint clean

## Download dependencies
deps:
	go mod tidy
	go mod download

## Build the binary
build: deps
	go build -o $(BINARY) $(CMD)

## Run directly (no binary)
run: deps
	go run $(CMD)/main.go

## Build and run
dev: build
	./$(BINARY)

## Tidy modules
tidy:
	go mod tidy

## Lint (requires golangci-lint)
lint:
	golangci-lint run ./...

## Clean build artefacts
clean:
	rm -f $(BINARY)
