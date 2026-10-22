# ============================================================================
# woma — Makefile
# ============================================================================
#
# Run `make help` (or just `make`) to see all available commands.
#
# GETTING STARTED (first time):
#   make setup          Install git hooks (pre-push runs the tests)
#   make install        Build and put woma in $(go env GOPATH)/bin
#
# DAILY DEVELOPMENT:
#   make build          Build ./woma (what ~/.local/bin/woma can link to)
#   make test           Run the tests (race detector on)
#   make check          gofmt, go vet, staticcheck, shellcheck, then the tests
#
# VERSIONING:
#   make version        Show the current version
#   make bump-patch     Bug fix release:      1.0.0 → 1.0.1
#   make bump-minor     New feature release:  1.0.0 → 1.1.0
#   make bump-major     Breaking change:      1.0.0 → 2.0.0
#
# RELEASING (run in order):
#   make bump-patch     1. Bump the version (pick patch/minor/major)
#   make prepare        2. Check, update CHANGELOG.md, commit
#   make release        3. Tag, push, GitHub release with binaries, Homebrew formula
#
# For the very first release (already at 0.1.0), skip the bump.
#
#   make undo-release   Undo the last release (GitHub release, tag, commit,
#                       formula)
#
# The tap is ../homebrew-tap (TAP=... to change it); releases publish with
# the carlosrberto gh account (GH_USER=... to change it, GH_USER= for gh's
# active one). docs/releasing.md has the details.
# ============================================================================

APP_NAME = woma
CUR_VERSION = $(shell sed -n 's/^var version = "\(.*\)"$$/\1/p' main.go)
TAP ?= ../homebrew-tap
GH_USER ?= carlosrberto
STATICCHECK ?= $(shell go env GOPATH)/bin/staticcheck
PLATFORMS = darwin/arm64 darwin/amd64 linux/arm64 linux/amd64

export TAP GH_USER

# --- Build ---

build: ## Build ./woma
	go build -o $(APP_NAME) .

install: ## Build and install into $(go env GOPATH)/bin
	go install .

test: ## Run the tests (race detector on)
	go test -race ./...

lint: ## gofmt, go vet, staticcheck and shellcheck
	@test -z "$$(gofmt -l .)" || { gofmt -l .; echo "gofmt: the files above need formatting" >&2; exit 1; }
	go vet ./...
	$(STATICCHECK) ./...
	shellcheck scripts/*.sh .githooks/pre-push

check: lint test ## Lint, then test

dist: ## Build the release archives and checksums.txt into dist/
	@rm -rf dist && mkdir -p dist
	@for p in $(PLATFORMS); do \
		os=$${p%/*}; arch=$${p#*/}; name=$(APP_NAME)_$(CUR_VERSION)_$${os}_$${arch}; \
		mkdir -p dist/$$name && \
		CGO_ENABLED=0 GOOS=$$os GOARCH=$$arch go build -trimpath \
			-ldflags "-s -w -X main.version=$(CUR_VERSION)" -o dist/$$name/$(APP_NAME) . && \
		cp LICENSE README.md dist/$$name/ && \
		tar -czf dist/$$name.tar.gz -C dist $$name && rm -rf dist/$$name && \
		echo "Created dist/$$name.tar.gz" || exit 1; \
	done
	@cd dist && shasum -a 256 *.tar.gz > checksums.txt

# --- Release ---

release-notes: ## Show the release notes from commits since the last tag
	@bash scripts/changelog.sh

prepare: ## Check, update CHANGELOG.md, commit "chore(release): vX"
	@test "$$(git branch --show-current)" = main || { echo "Not on main." >&2; exit 1; }
	@test -z "$$(git status --porcelain | grep -v ' main.go$$')" || { echo "Uncommitted changes besides main.go's version." >&2; exit 1; }
	@! git rev-parse -q --verify "refs/tags/v$(CUR_VERSION)" >/dev/null || { echo "v$(CUR_VERSION) is tagged already - bump the version first." >&2; exit 1; }
	$(MAKE) check
	@DATE=$$(date +%Y-%m-%d) && \
	BODY="$$(bash scripts/changelog.sh)" && \
	{ echo "# Changelog"; echo ""; echo "All notable changes to this project will be documented in this file."; echo ""; \
	  echo "## [$(CUR_VERSION)] - $$DATE"; echo ""; printf '%s\n' "$$BODY"; echo ""; \
	  tail -n +4 CHANGELOG.md; } > CHANGELOG.tmp && \
	mv CHANGELOG.tmp CHANGELOG.md && \
	echo "CHANGELOG.md updated"
	git add main.go CHANGELOG.md
	git commit -m "chore(release): v$(CUR_VERSION)"
	@echo "Ready. Run 'make release' to tag and publish."

release: ## Tag, push, create the GitHub release with binaries, update the Homebrew formula
	scripts/release.sh

undo-release: ## Undo the last release: GitHub release, tag, release commit, formula
	@echo "Undoing release v$(CUR_VERSION)…"
	-@if [ -n "$(GH_USER)" ]; then GH_TOKEN=$$(gh auth token --user $(GH_USER)); export GH_TOKEN; fi; \
		gh release delete "v$(CUR_VERSION)" --yes --cleanup-tag
	-@git tag -d "v$(CUR_VERSION)" 2>/dev/null
	-@rm -rf dist
	@if [ "$$(git log -1 --format=%s)" = "chore(release): v$(CUR_VERSION)" ]; then \
		echo "Dropping the 'chore(release): v$(CUR_VERSION)' commit and rewinding origin…"; \
		git reset --hard HEAD~1 && \
		git push --no-verify --force-with-lease origin "$$(git rev-parse --abbrev-ref HEAD)"; \
	else \
		echo "HEAD is not 'chore(release): v$(CUR_VERSION)' — left git history untouched."; \
	fi
	@if [ "$$(git -C $(TAP) log -1 --format=%s 2>/dev/null)" = "woma $(CUR_VERSION)" ]; then \
		echo "Reverting the formula in $(TAP)…"; \
		git -C $(TAP) revert --no-edit HEAD && git -C $(TAP) push -q; \
	else \
		echo "$(TAP)'s last commit is not 'woma $(CUR_VERSION)' — left the tap untouched."; \
	fi
	@echo "Undone v$(CUR_VERSION)."

formula: ## Print the Homebrew formula for the current version's tag
	@scripts/formula.sh v$(CUR_VERSION)

# --- Version ---

bump-patch: ## Bump patch version (1.0.0 → 1.0.1)
	@$(call do-bump,$$(echo $(CUR_VERSION) | awk -F. '{printf "%d.%d.%d", $$1, $$2, $$3+1}'))

bump-minor: ## Bump minor version (1.0.x → 1.1.0)
	@$(call do-bump,$$(echo $(CUR_VERSION) | awk -F. '{printf "%d.%d.0", $$1, $$2+1}'))

bump-major: ## Bump major version (1.x.x → 2.0.0)
	@$(call do-bump,$$(echo $(CUR_VERSION) | awk -F. '{printf "%d.0.0", $$1+1}'))

# Shared bump logic: updates the version in main.go
define do-bump
	NEW=$(1) && \
	sed 's/^var version = "$(CUR_VERSION)"$$/var version = "'$$NEW'"/' main.go > main.go.tmp && \
	mv main.go.tmp main.go && \
	echo "$(CUR_VERSION) → $$NEW"
endef

version: ## Show the current version
	@echo "$(CUR_VERSION)"

# --- Utilities ---

setup: hooks ## Install git hooks

hooks: ## Install git hooks (pre-push runs the tests)
	@git config core.hooksPath .githooks
	@echo "Git hooks installed (core.hooksPath = .githooks). Pre-push runs 'make test'."

# --- Help ---

help: ## Show this help
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) | sort | awk 'BEGIN {FS = ":.*?## "}; {printf "\033[36m%-14s\033[0m %s\n", $$1, $$2}'

.PHONY: build install test lint check dist release-notes prepare release undo-release formula bump-patch bump-minor bump-major version setup hooks help
.DEFAULT_GOAL := help
