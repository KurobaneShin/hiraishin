GO ?= go
BIN := bin/hiraishin

.PHONY: build test test-race lint vet e2e clean

build:
	CGO_ENABLED=0 $(GO) build -trimpath -o $(BIN) ./cmd/hiraishin

test:
	$(GO) test ./...

test-race:
	$(GO) test -race ./...

vet:
	$(GO) vet ./...

lint:
	golangci-lint run ./...

e2e:
	$(GO) test -race -run E2E ./e2e/...

clean:
	rm -rf bin
