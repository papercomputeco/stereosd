# Based around the auto-documented Makefile:
# http://marmelab.com/blog/2016/02/29/auto-documented-makefile.html
#
# hurls/Makefile — Run Hurl API tests against a running stereosd instance
# for testing internal admin operations.
#
# Usage:
#   make ping                                      # single test
#   make all                                       # all safe tests (no shutdown)
#   make secrets                                   # inject + list + delete cycle
#   STEREOSD_SOCK=/custom/path.sock make ping      # custom socket path

STEREOSD_SOCK ?= /run/stereos/stereosd.sock
HURL          ?= hurl
HURL_OPTS = --unix-socket $(STEREOSD_SOCK) --color --very-verbose

.PHONY: all
all: ping health list-secrets list-mounts agent-status ## Run all safe tests (no shutdown, no mount)

.PHONY: ping
ping: ## Ping stereosd
	$(HURL) $(HURL_OPTS) ping.hurl

.PHONY: health
health: ## Get health status
	$(HURL) $(HURL_OPTS) health.hurl

.PHONY: inject-secret
inject-secret: ## Inject a test secret
	$(HURL) $(HURL_OPTS) inject-secret.hurl

.PHONY: list-secrets
list-secrets: ## List injected secrets
	$(HURL) $(HURL_OPTS) list-secrets.hurl

.PHONY: delete-secret
delete-secret: ## Delete a test secret
	$(HURL) $(HURL_OPTS) delete-secret.hurl

.PHONY: secrets
secrets: inject-secret list-secrets delete-secret ## Full secret lifecycle (inject, list, delete)

.PHONY: mount
mount: ## Mount a shared directory (requires VM)
	$(HURL) $(HURL_OPTS) mount.hurl

.PHONY: list-mounts
list-mounts: ## List active mounts
	$(HURL) $(HURL_OPTS) list-mounts.hurl

.PHONY: agent-status
agent-status: ## List agent statuses (polled from agentd)
	$(HURL) $(HURL_OPTS) agent-status.hurl

.PHONY: shutdown
shutdown: ## Initiate graceful shutdown (CAUTION: will shut down the VM)
	$(HURL) $(HURL_OPTS) shutdown.hurl

.PHONY: help
help: ## Show available hurl targets
	@echo "Hurl API tests (socket: $(STEREOSD_SOCK))"
	@echo ""
	@grep -E '^[a-zA-Z0-9_-]+:.*?## .*$$' $(MAKEFILE_LIST) | \
		awk 'BEGIN {FS = ":.*?## "}; {printf "  \033[36m%-20s\033[0m %s\n", $$1, $$2}'
