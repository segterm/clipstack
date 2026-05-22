VERSION := $(shell git describe --tags --always --dirty 2>/dev/null || echo "dev")
GOOS    ?= linux
GOARCH  ?= amd64
LDFLAGS  = -s -w

.PHONY: build release clean

build:
	CGO_ENABLED=0 go build -ldflags="$(LDFLAGS)" -o clipd ./cmd/clipd
	CGO_ENABLED=0 go build -ldflags="$(LDFLAGS)" -o clip  ./cmd/clip

release:
	mkdir -p dist
	CGO_ENABLED=0 GOOS=$(GOOS) GOARCH=$(GOARCH) go build -ldflags="$(LDFLAGS)" -o dist/clipd ./cmd/clipd
	CGO_ENABLED=0 GOOS=$(GOOS) GOARCH=$(GOARCH) go build -ldflags="$(LDFLAGS)" -o dist/clip  ./cmd/clip
	tar -czf dist/clipstack-$(VERSION)-$(GOOS)-$(GOARCH).tar.gz -C dist clipd clip
	rm dist/clipd dist/clip
	@echo "→ dist/clipstack-$(VERSION)-$(GOOS)-$(GOARCH).tar.gz"

clean:
	rm -f clipd clip
	rm -rf dist/
