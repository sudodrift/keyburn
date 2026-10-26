VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -s -w -X main.version=$(VERSION)

.PHONY: build test lint check cross clean

build:
	CGO_ENABLED=0 go build -trimpath -ldflags '$(LDFLAGS)' -o bin/keyburn ./cmd/keyburn

test:
	go test -count=1 ./...

lint:
	@test -z "$$(gofmt -l .)" || { gofmt -l .; echo "run gofmt -w ."; exit 1; }
	go vet ./...

check: lint test

cross:
	for t in linux/amd64 linux/arm64 darwin/amd64 darwin/arm64; do \
		GOOS=$${t%/*} GOARCH=$${t#*/} CGO_ENABLED=0 go build -trimpath -ldflags '$(LDFLAGS)' \
			-o bin/keyburn-$${t%/*}-$${t#*/} ./cmd/keyburn || exit 1; \
	done

clean:
	rm -rf bin dist
