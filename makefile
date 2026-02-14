# Based around the auto-documented Makefile:
# http://marmelab.com/blog/2016/02/29/auto-documented-makefile.html

VERSION_PKG := github.com/papercomputeco/stereosd/pkg/version
GIT_COMMIT  := $(shell git rev-parse --short HEAD 2>/dev/null || echo "unknown")
BUILD_DATE  := $(shell date -u +"%Y-%m-%dT%H:%M:%SZ")
VERSION     ?= dev

LDFLAGS := -X $(VERSION_PKG).Version=$(VERSION) \
           -X $(VERSION_PKG).GitCommit=$(GIT_COMMIT) \
           -X $(VERSION_PKG).BuildDate=$(BUILD_DATE)

.PHONY: build
build: ## Builds artifact
	$(call print-target)
	@mkdir -p ./build
	CGO_ENABLED=0 go build -ldflags "$(LDFLAGS)" -o ./build/stereosd

.PHONY: test
test: ## Runs the test suite
	$(call print-target)
	go run github.com/onsi/ginkgo/v2/ginkgo -r --race ./...

.PHONY: nix-build
nix-build: ## Builds the Nix package for the current system
	$(call print-target)
	nix build --out-link ./build/result

.PHONY: clean
clean: ## Removes build artifacts
	$(call print-target)
	rm -rf ./build

.PHONY: help
.DEFAULT_GOAL := help
help: ## Prints this help message
	@grep -h -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) | awk 'BEGIN {FS = ":.*?## "}; {printf "\033[36m%-30s\033[0m %s\n", $$1, $$2}'

define print-target
    @printf "Executing target: \033[36m$@\033[0m\n"
endef
