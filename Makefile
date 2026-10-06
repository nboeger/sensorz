# sensorz - a terminal temperature and fan monitor for Linux.
#
# `make` builds ./sensorz, `make run` builds and runs it, `make test` runs the
# tests, `make check` is what CI should run.

BINARY  := sensorz
PKG     := ./cmd/sensorz
VERSION ?= $(shell sed -n "3s/version: *//p" snap/snapcraft.yaml 2>/dev/null || echo dev)
LDFLAGS := -s -w -X main.version=$(VERSION)

# The Go toolchain is not always on PATH: a gvm or asdf install puts it in a
# versioned directory, which is where a project-pinned toolchain lands too.
GO ?= $(shell command -v go 2>/dev/null || echo /usr/local/go/bin/go)

.PHONY: all build run install test race vet fmt fmtcheck lint check tidy clean help

all: build

## build: compile the binary at the top of the repository
build:
	$(GO) build -trimpath -ldflags '$(LDFLAGS)' -o $(BINARY) $(PKG)

## run: build and run the dashboard
run: build
	./$(BINARY)

## install: install the binary into GOBIN, or /usr/local/bin
install:
	$(GO) install -trimpath -ldflags '$(LDFLAGS)' $(PKG)

## test: run the tests
test:
	$(GO) test ./...

## race: run the tests under the race detector
race:
	$(GO) test -race ./...

## vet: run go vet
vet:
	$(GO) vet ./...

## fmt: format the source
fmt:
	$(GO) fmt ./...

## fmtcheck: fail if any source file is not gofmt clean
fmtcheck:
	@out=$$(gofmt -l cmd internal); \
	if [ -n "$$out" ]; then echo "not gofmt clean:"; echo "$$out"; exit 1; fi

## tidy: tidy go.mod
tidy:
	$(GO) mod tidy

## check: everything CI runs
check: fmtcheck vet test

## clean: remove build output
clean:
	rm -f $(BINARY)
	$(GO) clean

## help: list the targets
help:
	@grep -E '^## ' $(MAKEFILE_LIST) | sed 's/^## /  /'