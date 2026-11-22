#!/usr/bin/env make
# Two targets matter: build (host-side awf-gh) and check (prove the image works).

IMAGE  ?= opencode-dots:latest
COMPOSE ?= docker compose

.PHONY: build image check up down logs awf-gh

build: ## compile awf-gh from the sibling repo into bin/
	cd ~/projects/oss/awf-gh && go build -o ~/projects/perso/opencode-dots/bin/awf-gh .

image: build
	$(COMPOSE) build

check: image ## verify every binary and the serve flags before trusting the stack
	$(COMPOSE) run --rm --entrypoint bash opencode -c '\
	  opencode --version; \
	  awf --version; \
	  gh --version | head -1; \
	  awf-gh --help > /dev/null && echo "awf-gh ok"; \
	  awf serve --help'

up: image
	$(COMPOSE) up -d

down:
	$(COMPOSE) down

logs:
	$(COMPOSE) logs -f --tail=50

awf-gh: ## one polling pass, for testing by hand
	$(COMPOSE) --profile jobs run --rm awf-gh
