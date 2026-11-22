#!/usr/bin/env make
# Two targets matter: build (host-side awf-gh) and check (prove the image works).
# configure decides which piece runs locally and which runs in an image.

IMAGE  ?= opencode-dots:latest
COMPOSE ?= docker compose
# Where the awf-gh checkout lives. It is not published at its module path, so a
# local clone is the only source.
AWF_GH_SRC ?= $(HOME)/projects/oss/awf-gh

.PHONY: configure need-configure build image validate check up down logs awf-gh dashboard

configure: ## choose local or image for opencode, awf and gh; writes .env
	bin/configure

need-configure:
	@test -f .env -a -f compose.override.yaml || { echo "not configured: run 'make configure'" >&2; exit 1; }

build: ## compile awf-gh from $(AWF_GH_SRC) into bin/
	@test -d "$(AWF_GH_SRC)" || { echo "awf-gh source missing: set AWF_GH_SRC (now $(AWF_GH_SRC))" >&2; exit 1; }
	cd "$(AWF_GH_SRC)" && go build -o "$(CURDIR)/bin/awf-gh" .

image: build
	$(COMPOSE) --profile opencode-docker build opencode

validate: ## parse every workflow and stop on the first failure
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

awf-gh: .env ## one polling pass, for testing by hand
	$(COMPOSE) --profile jobs run --rm awf-gh

dashboard: ## serve the activity page on 127.0.0.1:8099
	go build -o bin/dashboard ./cmd/dashboard
	./bin/dashboard
