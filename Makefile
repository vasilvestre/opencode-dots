#!/usr/bin/env make
# Two targets matter: build (host-side awf-gh) and check (prove the image works).
# configure decides which piece runs locally and which runs in an image.

IMAGE  ?= opencode-dots:latest
COMPOSE ?= docker compose

.PHONY: configure need-configure build image check up down logs awf-gh dashboard

configure: ## choose local or image for opencode, awf and gh; writes .env
	bin/configure

need-configure:
	@test -f .env -a -f compose.override.yaml || { echo "not configured: run 'make configure'" >&2; exit 1; }

build: ## compile awf-gh from the sibling repo into bin/
	cd ~/projects/oss/awf-gh && go build -o ~/projects/perso/opencode-dots/bin/awf-gh .

image: build
	$(COMPOSE) --profile opencode-docker build opencode

validate: ## parse every workflow and stop on the first failure
	docker compose run --rm awf bash -c 'for f in /home/agent/.config/awf/workflows/*.yaml; do awf validate "$$(basename "$$f" .yaml)"; done'

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
