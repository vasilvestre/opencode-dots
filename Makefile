#!/usr/bin/env make
# Two targets matter: build (host-side awf-gh) and check (prove the image works).
# configure decides which piece runs locally and which runs in an image.

IMAGE  ?= opencode-dots:latest
COMPOSE ?= docker compose
# Where the awf-gh checkout lives. It is not published at its module path, so a
# local clone is the only source.
AWF_GH_SRC ?= $(HOME)/projects/oss/awf-gh

SERVER ?= $(DOTS_SERVER)
TUNNEL_PORT ?= 8097
EXECUTOR_PORT ?= 2512
.PHONY: configure need-configure build image validate adr check up down logs awf-gh dashboard health lint repos deploy-test deploy-offline-test shim-test workspace-test held-test watchers-test tend-test propose-links-test script-tests go-build hooks ci tunnel executor-tunnel

configure: ## choose local or image for opencode, awf, gh, awf-gh and (optional) claude; writes .env
	bin/configure

lint: ## lint the dashboard frontend, report unused code
	npm run --silent lint

need-configure:
	@test -f .env -a -f compose.override.yaml || { echo "not configured: run 'make configure'" >&2; exit 1; }

build: ## compile awf-gh from $(AWF_GH_SRC) into bin/
	@test -d "$(AWF_GH_SRC)" || { echo "awf-gh source missing: set AWF_GH_SRC (now $(AWF_GH_SRC))" >&2; exit 1; }
	cd "$(AWF_GH_SRC)" && go build -o "$(CURDIR)/bin/awf-gh" .

image: build
	$(COMPOSE) --profile opencode-docker build opencode

adr: ## check ADR numbering, index rows and supersede links
	bin/adr check

repos: ## regenerate awf-gh.yaml, tend.list, repos.map, sync.map and breaker.list from repos.yaml
	go build -o bin/repos ./cmd/repos
	bin/repos generate

watchers-test: ## check bin/watchers on two directories of generated watcher files
	test/watchers.sh

workspace-test: ## run bin/workspace against a local bare repository (nothing leaves the machine)
	test/workspace.sh

held-test: ## check bin/held exit codes against a stub
	test/held.sh
	test/headrepo.sh

tend-test: ## check what bin/tend posts to awf serve, with a fake curl
	test/tend.sh

propose-links-test: ## check what bin/propose-links posts to awf serve, with a fake curl
	test/propose-links.sh

shim-test: ## run the opencode shim against a stub (no model is called)
	test/shim.sh

script-tests: ## run bin/test-publish-gate, bin/test-classify, bin/test-sandbox, bin/test-dot-sign and bin/test-budget (offline; container checks only with a local dots-* image)
	bin/test-publish-gate
	bin/test-classify
	bin/test-sandbox
	bin/test-dot-sign
	bin/test-budget
	bin/test-pickup
	bin/test-tick
	bin/test-pause
	bin/test-gate-sandbox
	bin/test-where
	bin/test-dots-units

deploy-offline-test: ## test mcp.sh, skills.sh and the opencode config offline (no docker, no network)
	deploy/test/offline.sh

deploy-test: ## run the deploy scripts in a Debian 13 container (needs docker and the network)
	deploy/test/offline.sh
	deploy/test/run.sh

validate: adr ## parse every workflow and stop on the first failure
	$(COMPOSE) run --rm awf awf validate --dir /home/agent/.config/awf/workflows/

check: image validate ## verify every binary and the serve flags before trusting the stack
	$(COMPOSE) --profile opencode-docker run --rm --entrypoint bash opencode -c '\
	  opencode --version; \
	  awf --version; \
	  gh --version | head -1; \
	  awf-gh --help > /dev/null && echo "awf-gh ok"; \
	  repos 2>&1 | head -1 | grep -q usage && echo "repos ok"; \
	  awf serve --help'

up: image need-configure
	$(COMPOSE) up -d

down:
	$(COMPOSE) down

logs:
	$(COMPOSE) logs -f --tail=50

awf-gh: .env ## one polling pass, for testing by hand (native when DOT_AWFGH=local)
	@if grep -qx 'DOT_AWFGH=local' .env; then \
	  set -a; . ./.env; set +a; bin/awf-gh --config awf-gh.yaml; \
	else $(COMPOSE) --profile jobs run --rm awf-gh; fi

sync: ## fast-forward the managed clones onto origin/main
	./bin/sync

dashboard: ## serve the activity page on 127.0.0.1:8099
	go build -o bin/dashboard ./cmd/dashboard
	./bin/dashboard

health: ## build bin/health and run it: exit 0 healthy, 1 degraded, 2 down
	go build -o bin/health ./cmd/health
	go build -o bin/dots ./cmd/dots
	./bin/health

go-build: ## build every Go command into bin/ (a bare `go build` inside cmd/NAME drops a binary there)
	go build -o bin/dashboard ./cmd/dashboard
	go build -o bin/repos ./cmd/repos
	go build -o bin/health ./cmd/health
	go build -o bin/worker ./cmd/worker
	go build -o bin/dots ./cmd/dots

hooks: ## use the tracked git hooks of this clone: refuses a commit that adds a binary
	git config core.hooksPath .githooks

tunnel: ## owner view of a server's dashboard on http://127.0.0.1:$(TUNNEL_PORT): make tunnel SERVER=user@host
	@test -n "$(SERVER)" || { echo "usage: make tunnel SERVER=user@host (or export DOTS_SERVER)"; exit 2; }
	@echo "owner view on http://127.0.0.1:$(TUNNEL_PORT) (Ctrl-C to close)"
	ssh -N -o ExitOnForwardFailure=yes -L 127.0.0.1:$(TUNNEL_PORT):127.0.0.1:8099 $(SERVER)

executor-tunnel: ## legacy push model, replaced by the job queue (ADR 0048, docs/executors.md): a reverse tunnel so a server reaches this awf
	@test -n "$(SERVER)" || { echo "usage: make executor-tunnel SERVER=user@host (or export DOTS_SERVER)"; exit 2; }
	@echo "server 127.0.0.1:$(EXECUTOR_PORT) -> this machine's awf serve on 127.0.0.1:2511 (Ctrl-C to close)"
	ssh -N -o ExitOnForwardFailure=yes -o ServerAliveInterval=30 -R 127.0.0.1:$(EXECUTOR_PORT):127.0.0.1:2511 $(SERVER)

ci: ## run every step of .github/workflows/ci.yml locally; run it before pushing
	go vet ./...
	golangci-lint run ./...
	go test ./...
	bin/adr check
	bin/check-binaries tracked
	test/binaries.sh
	test/backup.sh
	test/shim.sh
	test/workspace.sh
	test/watchers.sh
	test/held.sh
	test/headrepo.sh
	test/model.sh
	test/tend.sh
	test/propose-links.sh
	$(MAKE) script-tests
	deploy/test/offline.sh
	deslop-please ./...
