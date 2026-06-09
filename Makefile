.PHONY: build test fmt fmt-check vet ci install release-smoke readme-html readme-html-check clean

GO_FILES := $(shell find . -name '*.go' -not -path './vendor/*')
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo "dev")

# README.html is a generated render of README.md. PANDOC_CMD is the single
# source of truth for how it is built, shared by the regenerate and freshness
# targets so they cannot diverge.
PANDOC_CMD = pandoc README.md -f gfm -t html5 -s --toc --toc-depth=2 \
	--metadata title="amq-squad — role-aware agent team launcher" \
	--include-in-header=docs/readme-head.html

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

ci: fmt-check vet test readme-html-check

# Regenerate the browsable README.html from README.md. Run this whenever
# README.md changes (the release process bumps README.md, so it runs here).
readme-html:
	@command -v pandoc >/dev/null 2>&1 || { echo "pandoc is required: brew install pandoc" >&2; exit 1; }
	$(PANDOC_CMD) -o README.html
	@echo "regenerated README.html"

# Fail if README.html is stale relative to README.md (drift guard, part of ci so
# a release PR cannot merge a README.md change without the matching README.html).
# Skips cleanly when pandoc is absent so pandoc-less CI is not blocked.
readme-html-check:
	@if ! command -v pandoc >/dev/null 2>&1; then \
		echo "pandoc not found; skipping README.html freshness check"; \
	else \
		tmp="$$(mktemp)"; \
		trap 'rm -f "$$tmp"' EXIT; \
		$(PANDOC_CMD) -o "$$tmp"; \
		if ! diff -q "$$tmp" README.html >/dev/null 2>&1; then \
			echo "README.html is stale: run 'make readme-html' and commit it" >&2; \
			exit 1; \
		fi; \
		echo "README.html is in sync with README.md"; \
	fi

install: build
	install amq-squad $(GOPATH)/bin/amq-squad 2>/dev/null || install amq-squad $(HOME)/go/bin/amq-squad

release-smoke:
	@test "$(VERSION)" != "dev" || (echo "VERSION is required, for example VERSION=v0.5.1" >&2; exit 1)
	@tmp="$$(mktemp -d)"; \
	trap 'rm -rf "$$tmp"' EXIT; \
	GOBIN="$$tmp" GOPROXY=direct go install github.com/omriariav/amq-squad/cmd/amq-squad@$(VERSION); \
	got="$$("$$tmp/amq-squad" version)"; \
	want="amq-squad $(VERSION)"; \
	if [ "$$got" != "$$want" ]; then \
		echo "version mismatch: got '$$got', want '$$want'" >&2; \
		exit 1; \
	fi; \
	echo "$$got"

clean:
	rm -f amq-squad
