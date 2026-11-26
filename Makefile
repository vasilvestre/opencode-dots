#!/usr/bin/env make
# Two targets matter: build (host-side awf-gh) and check (prove the image works).
# configure decides which piece runs locally and which runs in an image.

IMAGE  ?= opencode-dots:latest
COMPOSE ?= docker compose
# Where the awf-gh checkout lives. It is not published at its module path, so a
# local clone is the only source.
AWF_GH_SRC ?= $(HOME)/projects/oss/awf-gh

.PHONY: configure need-configure build image validate adr check up down logs awf-gh dashboard lint repos deploy-test shim-test workspace-test held-test watchers-test

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

repos: ## regenerate awf-gh.yaml, tend.list, repos.map and sync.map from repos.yaml
	go build -o bin/repos ./cmd/repos
	bin/repos generate

watchers-test: ## check bin/watchers on two directories of generated watcher files
	test/watchers.sh

workspace-test: ## run bin/workspace against a local bare repository (nothing leaves the machine)
	test/workspace.sh

held-test: ## check bin/held exit codes against a stub
	test/held.sh

shim-test: ## run the opencode shim against a stub (no model is called)
	test/shim.sh

deploy-test: ## run the deploy scripts in a Debian 13 container (needs docker and the network)
	deploy/test/run.sh

validate: adr ## parse every workflow and stop on the first failure
	$(COMPOSE) run --rm awf awf validate --dir /home/agent/.config/awf/workflows/

check: image validate ## verify every binary and the serve flags before trusting the stack
	$(COMPOSE) --profile opencode-docker run --rm --entrypoint bash opencode -c '\
	  opencode --version; \
	  awf --version; \
	  gh --version | head -1; \
	  awf-gh --help > /dev/null && echo "awf-gh ok"; \
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
