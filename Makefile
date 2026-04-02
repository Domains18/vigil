.PHONY: test build lint tidy

build:
	go build ./...
	cd ginmw && go build ./...

test:
	go test ./... -timeout 30s
	cd ginmw && go test ./... -timeout 30s

test-verbose:
	go test ./... -v -timeout 30s
	cd ginmw && go test ./... -v -timeout 30s

tidy:
	go mod tidy
	cd ginmw && go mod tidy

lint:
	go vet ./...
	cd ginmw && go vet ./...
