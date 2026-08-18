GO ?= go
BIN := bin/gandalf

.PHONY: all build test vet fmt fmt-check check clean

all: check build

build:
	$(GO) build -o $(BIN) ./cmd/gandalf

test:
	$(GO) test ./...

vet:
	$(GO) vet ./...

fmt:
	$(GO) fmt ./...

fmt-check:
	@unformatted=$$(gofmt -l .); \
	if [ -n "$$unformatted" ]; then echo "unformatted files:"; echo "$$unformatted"; exit 1; fi

check: fmt-check vet test

clean:
	rm -rf bin
