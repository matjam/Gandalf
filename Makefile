GO ?= go
BIN := bin/gandalf

GOOS := $(shell $(GO) env GOOS)
GOARCH := $(shell $(GO) env GOARCH)

# ONNX Runtime is roughly fourteen times faster than the pure-Go compute
# backend — a vault that takes twenty minutes to index takes ninety seconds.
# It needs two native pieces: the onnxruntime shared library, and the Rust
# tokenizer as a static archive linked at build time.
#
# On macOS both are a short step away, so ORT is the default there and the
# build fetches what it can. Everywhere else the pure-Go backend is the
# default, because a build that fails on a fresh clone is worse than a slow
# index. Force either with `make build ORT=1` or `make build ORT=0`.
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

ifeq ($(GOOS),darwin)
ORT ?= 1
else
ORT ?= 0
endif

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
# toolchain. The pure-Go backend is the default rather than a failed build.

.PHONY: all build build-ort build-go test vet fmt fmt-check check clean deps doctor-deps

all: check build

# build picks the fast path when this platform wants it and the pieces are
# present, and says which one it took either way. Silently producing a binary
# fourteen times slower than the one you asked for is the failure worth
# avoiding here.
build:
ifeq ($(ORT),1)
	@if [ -z "$(TOKENIZERS_ASSET)" ]; then \
		echo "no prebuilt tokenizer for $(GOOS)/$(GOARCH); building without ONNX Runtime"; \
		$(MAKE) build-go; \
	elif [ -z "$(ONNXRUNTIME_DIR)" ]; then \
		echo "onnxruntime not found (try: brew install onnxruntime); building without it"; \
		$(MAKE) build-go; \
	else \
		$(MAKE) build-ort; \
	fi
else
	@$(MAKE) build-go
endif

build-go:
	@echo "building $(BIN) with the pure-Go backend"
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
