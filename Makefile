.PHONY: build test fmt fmt-check vet ci install clean

GO_FILES := $(shell find . -name '*.go' -not -path './vendor/*')
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo "dev")

build:
	go build -ldflags "-X main.version=$(VERSION)" -o amq-squad ./cmd/amq-squad

test:
	go test ./...

fmt:
	gofmt -w $(GO_FILES)

fmt-check:
	@test -z "$(shell gofmt -l $(GO_FILES))"

vet:
	go vet ./...

ci: fmt-check vet test

install: build
	install amq-squad $(GOPATH)/bin/amq-squad 2>/dev/null || install amq-squad $(HOME)/go/bin/amq-squad

clean:
	rm -f amq-squad
