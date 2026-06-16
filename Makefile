# kite-kvm — KVM control node (被控节点)

BINARY      := kite-kvm
PKG         := ./cmd/kite-kvm
VERSION     ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS     := -s -w -X main.version=$(VERSION)
GOARCH      ?= amd64

.PHONY: all build build-linux run test vet fmt tidy docs clean

all: build

## build: build for the host platform
build:
	go build -ldflags '$(LDFLAGS)' -o bin/$(BINARY) $(PKG)

## build-linux: static, cgo-free Linux binary (the deploy target)
build-linux:
	CGO_ENABLED=0 GOOS=linux GOARCH=$(GOARCH) \
		go build -ldflags '$(LDFLAGS)' -o bin/$(BINARY)-linux-$(GOARCH) $(PKG)

## run: run the agent locally
run:
	go run $(PKG)

## test: run unit tests
test:
	go test ./...

## vet: run go vet
vet:
	go vet ./...

## fmt: format the source
fmt:
	gofmt -s -w .

## tidy: tidy go.mod/go.sum
tidy:
	go mod tidy

## docs: render the OpenAPI spec to a self-contained HTML (needs node/npx)
docs:
	npx --yes redoc-cli@0.13.21 bundle docs/openapi.yaml -o docs/api.html

## clean: remove build artifacts
clean:
	rm -rf bin
