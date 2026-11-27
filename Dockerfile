# One image, five binaries: nothing lands in ~/.local/bin on this machine.

# awf v0.11.10 cannot preflight opencode v2: it runs `opencode auth list`
# without --format json and expects an "N credentials" footer that v2 dropped.
# v0.12.0 parses the JSON form and records the cost of every agent step.
FROM golang:1.24-bookworm AS awf-build
# The module requires go 1.26; the official image pins GOTOOLCHAIN=local.
ENV GOTOOLCHAIN=auto
ARG AWF_REF=v0.12.0
RUN CGO_ENABLED=0 go install github.com/awf-project/cli/cmd/awf@${AWF_REF}

# bin/repos: the workflows that read the database (_propose-links) call it. Built here,
# statically, so the layer is the binary alone and nothing is fetched at run time.
FROM golang:1.24-bookworm AS repos-build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY internal/ internal/
COPY cmd/repos/ cmd/repos/
COPY cmd/dots/ cmd/dots/
RUN CGO_ENABLED=0 go build -trimpath -ldflags='-s -w' -o /out/repos ./cmd/repos \
 && CGO_ENABLED=0 go build -trimpath -ldflags='-s -w' -o /out/dots ./cmd/dots

FROM debian:stable-slim

ARG TARGETARCH=amd64
ARG TOWER_VERSION=v0.1.2
# 0 = skip tower, 1 = include it (it is 0.1.0, opt in on purpose)
ARG WITH_TOWER=0

ENV DEBIAN_FRONTEND=noninteractive \
    HOME=/home/agent \
    PATH=/opt/agent/bin:/home/agent/.opencode/bin:$PATH

RUN apt-get update && apt-get install -y --no-install-recommends \
      bash ca-certificates curl git jq nodejs npm ripgrep tini unzip \
    && rm -rf /var/lib/apt/lists/* \
    && mkdir -p /opt/agent/bin && useradd -m -u 1000 agent

# awf
COPY --from=awf-build /go/bin/awf /opt/agent/bin/awf

# gh: no apt repo, just the release tarball
RUN set -eux; \
    case "${TARGETARCH}" in amd64) GHARCH=amd64 ;; arm64) GHARCH=arm64 ;; esac; \
    GHVER=$(curl -fsSL https://api.github.com/repos/cli/cli/releases/latest | jq -r .tag_name | sed 's/^v//'); \
    curl -fsSL -o /tmp/gh.tgz "https://github.com/cli/cli/releases/download/v${GHVER}/gh_${GHVER}_linux_${GHARCH}.tar.gz"; \
    tar -xzf /tmp/gh.tgz -C /tmp; \
    mv /tmp/gh_${GHVER}_linux_${GHARCH}/bin/gh /opt/agent/bin/gh

# tower: opt-in, prebuilt linux tarball. It carries the binary and its extensions,
# and a daemon that spins at 100% CPU once fixed the idle watcher (v0.1.1).
RUN set -eux; \
    if [ "${WITH_TOWER}" = "1" ]; then \
      case "${TARGETARCH}" in amd64) TARCH=x86_64-unknown-linux-gnu ;; arm64) TARCH=aarch64-unknown-linux-gnu ;; esac; \
      curl -fsSL -o /tmp/tower.tgz "https://github.com/awf-project/tower/releases/download/${TOWER_VERSION}/tower-${TOWER_VERSION}-${TARCH}.tar.gz"; \
      mkdir -p /opt/tower; \
      tar -xzf /tmp/tower.tgz -C /opt/tower; \
      install -m 755 /opt/tower/tower /opt/agent/bin/tower; \
    fi

# Read by tower as its extension search dir, next to ~/.local/share/tower/extensions.
ENV TOWER_EXTENSIONS_DIR=/opt/tower/extensions

# awf-gh: built on the host by `make build`, copied here
COPY bin/ /opt/agent/bin/
RUN chmod +x /opt/agent/bin/*

# repos: after the COPY above, so a host-built bin/repos never replaces the image's own.
COPY --from=repos-build /out/repos /opt/agent/bin/repos
COPY --from=repos-build /out/dots /opt/agent/bin/dots
ENV REPOS_BIN=/opt/agent/bin/repos

# Pre-create every bind-mount target: Docker creates missing parents as root,
# and then the agent cannot write next to them.
RUN mkdir -p /home/agent/.local/share/opencode /home/agent/.local/state \
             /home/agent/.config/gh /home/agent/.config/opencode /home/agent/.claude /state \
    && chown -R agent:agent /home/agent /state

USER agent
WORKDIR /home/agent

# The official installer writes to ~/.opencode/bin, which stays in the image.
RUN curl -fsSL https://opencode.ai/install | bash

# npm global installs land under $HOME/.npm-global (no root needed).
ENV NPM_CONFIG_PREFIX=/home/agent/.npm-global \
    PATH=/home/agent/.npm-global/bin:/opt/agent/bin:/home/agent/.opencode/bin:$PATH
RUN npm install -g @anthropic-ai/claude-code

ENTRYPOINT ["/usr/bin/tini", "--"]
CMD ["bash"]
