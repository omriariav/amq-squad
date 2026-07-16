.PHONY: build test fmt fmt-check vet ci install release-check release-validator-test release-smoke readme-html readme-html-check docs-html docs-html-check html skills-generate skills-check skill-routing-check dogfood-claude clean

GO_FILES := $(shell find . -name '*.go' -not -path './vendor/*')
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo "dev")

# README.html is a generated render of README.md. PANDOC_CMD is the single
# source of truth for how it is built, shared by the regenerate and freshness
# targets so they cannot diverge.
PANDOC_CMD = pandoc README.md -f gfm -t html5 -s --toc --toc-depth=2 \
	--metadata title="amq-squad — role-aware agent team launcher" \
	--include-in-header=docs/readme-head.html

# docs/skills.html is a generated render of docs/skills.md (the deep skills
# guide). SKILLS_PANDOC_CMD mirrors PANDOC_CMD so the regenerate and freshness
# targets cannot diverge.
SKILLS_PANDOC_CMD = pandoc docs/skills.md -f gfm -t html5 -s --toc --toc-depth=2 \
	--metadata title="amq-squad — using the skills" \
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

ci: fmt-check vet test readme-html-check docs-html-check skills-check skill-routing-check release-validator-test

# Regenerate plugin SKILL.md mirrors from plugins/skills-src.
skills-generate:
	@command -v python3 >/dev/null 2>&1 || { echo "python3 is required for skills-check" >&2; exit 1; }
	@python3 scripts/generate-plugin-skills.py

# Validate the generated plugin SKILL.md mirrors and their YAML frontmatter. A
# stray unquoted colon-space in a description makes the loader silently skip the
# skill (it shipped once in 1.9.1 and broke Codex orchestration), so this is a
# ci gate.
skills-check:
	@command -v python3 >/dev/null 2>&1 || { echo "python3 is required for skills-check" >&2; exit 1; }
	@python3 scripts/generate-plugin-skills.py --check
	@python3 scripts/check-skill-frontmatter.py

# Current public docs and generated team rules must teach the authoritative
# namespaced skills. Historical release notes may retain old names, but active
# invocation examples cannot regress to compatibility redirects.
skill-routing-check:
	@command -v python3 >/dev/null 2>&1 || { echo "python3 is required for skill-routing-check" >&2; exit 1; }
	@python3 scripts/check-current-skill-routing.py

release-check:
	@test "$(VERSION)" != "dev" || (echo "VERSION is required, for example VERSION=v2.8.1" >&2; exit 1)
	@command -v python3 >/dev/null 2>&1 || { echo "python3 is required for release-check" >&2; exit 1; }
	@python3 scripts/check-release-version.py "$(VERSION)"
	@python3 scripts/check-current-skill-routing.py

release-validator-test:
	@command -v python3 >/dev/null 2>&1 || { echo "python3 is required for release-validator-test" >&2; exit 1; }
	@python3 scripts/test_check_release_version.py

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

# Regenerate docs/skills.html from docs/skills.md. Run whenever the skills guide
# changes (the release process bumps docs alongside README).
docs-html:
	@command -v pandoc >/dev/null 2>&1 || { echo "pandoc is required: brew install pandoc" >&2; exit 1; }
	$(SKILLS_PANDOC_CMD) -o docs/skills.html
	@echo "regenerated docs/skills.html"

# Fail if docs/skills.html is stale relative to docs/skills.md (drift guard, part
# of ci). Skips cleanly when pandoc is absent so pandoc-less CI is not blocked.
docs-html-check:
	@if ! command -v pandoc >/dev/null 2>&1; then \
		echo "pandoc not found; skipping docs/skills.html freshness check"; \
	else \
		tmp="$$(mktemp)"; \
		trap 'rm -f "$$tmp"' EXIT; \
		$(SKILLS_PANDOC_CMD) -o "$$tmp"; \
		if ! diff -q "$$tmp" docs/skills.html >/dev/null 2>&1; then \
			echo "docs/skills.html is stale: run 'make docs-html' and commit it" >&2; \
			exit 1; \
		fi; \
		echo "docs/skills.html is in sync with docs/skills.md"; \
	fi

# Regenerate every browsable HTML render.
html: readme-html docs-html

install: build
	install amq-squad $(GOPATH)/bin/amq-squad 2>/dev/null || install amq-squad $(HOME)/go/bin/amq-squad

# Launch Claude Code with the plugin loaded live from this working tree.
# Shadows the marketplace-installed amq-squad plugin for this session only;
# other folders keep the GitHub-marketplace version.
dogfood-claude:
	claude --plugin-dir plugins/claude

release-smoke:
	@test "$(VERSION)" != "dev" || (echo "VERSION is required, for example VERSION=v0.5.1" >&2; exit 1)
	@$(MAKE) --no-print-directory release-check VERSION="$(VERSION)"
	@tmp="$$(mktemp -d)"; \
	trap 'rm -rf "$$tmp"' EXIT; \
	GOBIN="$$tmp" GOPROXY=direct go install github.com/omriariav/amq-squad/v2/cmd/amq-squad@$(VERSION); \
	got="$$("$$tmp/amq-squad" version)"; \
	want="amq-squad $(VERSION)"; \
	if [ "$$got" != "$$want" ]; then \
		echo "version mismatch: got '$$got', want '$$want'" >&2; \
		exit 1; \
	fi; \
	echo "$$got"

clean:
	rm -f amq-squad
