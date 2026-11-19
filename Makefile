VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -s -w -X main.version=$(VERSION)
PKG     := ./cmd/nutwake
DIST    := dist

.PHONY: build test vet fmt-check check cross site clean

build:
	go build -ldflags '$(LDFLAGS)' -o nutwake $(PKG)

test:
	go test -race ./...

vet:
	go vet ./...

fmt-check:
	@test -z "$$(gofmt -l .)" || { echo 'gofmt needed on:'; gofmt -l .; exit 1; }

check: fmt-check vet test

# Static binaries for a 64-bit Pi (arm64), a 32-bit Pi OS (armv7) and x86-64.
cross:
	mkdir -p $(DIST)
	GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -ldflags '$(LDFLAGS)' -o $(DIST)/nutwake-linux-amd64 $(PKG)
	GOOS=linux GOARCH=arm64 CGO_ENABLED=0 go build -ldflags '$(LDFLAGS)' -o $(DIST)/nutwake-linux-arm64 $(PKG)
	GOOS=linux GOARCH=arm GOARM=7 CGO_ENABLED=0 go build -ldflags '$(LDFLAGS)' -o $(DIST)/nutwake-linux-armv7 $(PKG)

# The website: the landing page and the guide in docs/guide, built into _site/ and checked.
# Needs Python 3 and: pip install -r site/requirements.txt
site:
	python3 site/build.py
	python3 site/check.py

clean:
	rm -rf $(DIST) nutwake _site
