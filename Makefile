GO ?= go
BIN := bin/gandalf

GOOS := $(shell $(GO) env GOOS)
GOARCH := $(shell $(GO) env GOARCH)

# Local inference requires ONNX Runtime and the Rust tokenizer archive.
TOKENIZERS_VERSION := v1.27.0
LIBDIR := .build/lib
TOKENIZERS_LIB := $(LIBDIR)/libtokenizers.a

# The library directory the loader will be pointed at. Homebrew's prefix is not
# on the default search path, which is why it is named rather than assumed.
# Kept in step with ortSearchDirs and ortLibraryNames in internal/embed: the
# build and the binary must agree on where the library is, or make reports the
# fast path as available and the binary then falls back at runtime.
ONNXRUNTIME_DIR := $(shell \
	if [ -n "$$GANDALF_ONNXRUNTIME" ]; then echo "$$GANDALF_ONNXRUNTIME"; else \
	for d in /opt/homebrew/lib /usr/local/lib /usr/lib /usr/lib/x86_64-linux-gnu /usr/lib/aarch64-linux-gnu; do \
		for f in libonnxruntime.dylib libonnxruntime.1.dylib libonnxruntime.so libonnxruntime.so.1; do \
			if [ -f "$$d/$$f" ]; then echo "$$d"; exit 0; fi; \
		done; \
	done; fi)

ORT ?= 1

# The prebuilt tokenizer archive is published per platform. darwin/arm64 and
# friends are all released; anything not listed has to build it from source.
ifeq ($(GOOS)/$(GOARCH),darwin/arm64)
TOKENIZERS_ASSET := libtokenizers.darwin-arm64.tar.gz
endif
ifeq ($(GOOS)/$(GOARCH),darwin/amd64)
TOKENIZERS_ASSET := libtokenizers.darwin-x86_64.tar.gz
endif
ifeq ($(GOOS)/$(GOARCH),linux/arm64)
TOKENIZERS_ASSET := libtokenizers.linux-arm64.tar.gz
endif
ifeq ($(GOOS)/$(GOARCH),linux/amd64)
TOKENIZERS_ASSET := libtokenizers.linux-amd64.tar.gz
endif
ifeq ($(GOOS)/$(GOARCH),linux/ppc64le)
TOKENIZERS_ASSET := libtokenizers.linux-ppc64le.tar.gz
endif
ifeq ($(GOOS)/$(GOARCH),linux/s390x)
TOKENIZERS_ASSET := libtokenizers.linux-s390x.tar.gz
endif
# Windows is absent deliberately: onnxruntime publishes Windows binaries but
# the tokenizer archive does not, so the fast path there needs a Rust
# toolchain. Builds without ORT support HTTP embeddings or disabled search.

.PHONY: all build build-ort build-no-local test vet fmt fmt-check check clean deps doctor-deps

all: check build

# Missing native dependencies fail explicitly instead of changing backends.
build:
ifeq ($(ORT),1)
	@if [ -z "$(TOKENIZERS_ASSET)" ]; then \
		echo "no prebuilt tokenizer for $(GOOS)/$(GOARCH); build libtokenizers.a manually or use make build ORT=0 for HTTP embeddings"; exit 1; \
	elif [ -z "$(ONNXRUNTIME_DIR)" ]; then \
		echo "onnxruntime not found; install it or set GANDALF_ONNXRUNTIME"; exit 1; \
	else \
		$(MAKE) build-ort; \
	fi
else
	@$(MAKE) build-no-local
endif

build-no-local:
	@echo "building $(BIN) without local inference; use -embed http or -embed none"
	$(GO) build -o $(BIN) ./cmd/gandalf

build-ort: $(TOKENIZERS_LIB)
	@echo "building $(BIN) with ONNX Runtime from $(ONNXRUNTIME_DIR)"
	CGO_LDFLAGS="-L$(CURDIR)/$(LIBDIR)" $(GO) build -tags ORT -o $(BIN) ./cmd/gandalf

# deps fetches the prebuilt tokenizer archive. It is a build-time static
# library, so it lives with the build output rather than being installed.
deps: $(TOKENIZERS_LIB)

$(TOKENIZERS_LIB):
	@if [ -z "$(TOKENIZERS_ASSET)" ]; then \
		echo "no prebuilt tokenizer for $(GOOS)/$(GOARCH); see https://github.com/daulet/tokenizers"; exit 1; \
	fi
	@mkdir -p $(LIBDIR)
	@echo "fetching $(TOKENIZERS_ASSET) $(TOKENIZERS_VERSION)"
	@curl -sSfL -o $(LIBDIR)/tokenizers.tar.gz \
		https://github.com/daulet/tokenizers/releases/download/$(TOKENIZERS_VERSION)/$(TOKENIZERS_ASSET)
	@tar xzf $(LIBDIR)/tokenizers.tar.gz -C $(LIBDIR)
	@rm -f $(LIBDIR)/tokenizers.tar.gz

# doctor-deps says what the fast path is missing, without building anything.
doctor-deps:
	@echo "platform:      $(GOOS)/$(GOARCH)"
	@echo "ORT requested: $(ORT)"
	@if [ -n "$(ONNXRUNTIME_DIR)" ]; then echo "onnxruntime:   $(ONNXRUNTIME_DIR)"; \
		else echo "onnxruntime:   missing (brew install onnxruntime)"; fi
	@if [ -f "$(TOKENIZERS_LIB)" ]; then echo "tokenizers:    $(TOKENIZERS_LIB)"; \
		elif [ -n "$(TOKENIZERS_ASSET)" ]; then echo "tokenizers:    missing (make deps)"; \
		else echo "tokenizers:    no prebuilt for this platform"; fi

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
	rm -rf bin .build
