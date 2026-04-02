.PHONY: test build lint tidy

build:
	go build ./...

test:
	go test ./... -timeout 30s

test-verbose:
	go test ./... -v -timeout 30s

tidy:
	go mod tidy

lint:
	go vet ./...
