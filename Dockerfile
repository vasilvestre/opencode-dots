# One image, four binaries: nothing lands in ~/.local/bin on pop-os.
FROM debian:stable-slim

ARG TARGETARCH=amd64
ARG AWF_VERSION=v0.11.10
ARG TOWER_VERSION=v0.1.0
# 0 = skip tower, 1 = include it (it is 0.1.0, opt in on purpose)
ARG WITH_TOWER=0

ENV DEBIAN_FRONTEND=noninteractive \
    HOME=/home/agent \
    PATH=/opt/agent/bin:/home/agent/.opencode/bin:$PATH

RUN apt-get update && apt-get install -y --no-install-recommends \
      bash ca-certificates curl git jq ripgrep tini unzip \
    && rm -rf /var/lib/apt/lists/* \
    && mkdir -p /opt/agent/bin && useradd -m -u 1000 agent

# awf
RUN set -eux; \
    curl -fsSL -o /tmp/awf.tgz "https://github.com/awf-project/cli/releases/download/${AWF_VERSION}/awf_linux_${TARGETARCH}.tar.gz"; \
    tar -xzf /tmp/awf.tgz -C /tmp; \
    find /tmp -type f -name awf -perm -u+x -exec mv {} /opt/agent/bin/awf \; ; \
    chmod +x /opt/agent/bin/awf

# gh: no apt repo, just the release tarball
RUN set -eux; \
    case "${TARGETARCH}" in amd64) GHARCH=amd64 ;; arm64) GHARCH=arm64 ;; esac; \
    GHVER=$(curl -fsSL https://api.github.com/repos/cli/cli/releases/latest | jq -r .tag_name | sed 's/^v//'); \
    curl -fsSL -o /tmp/gh.tgz "https://github.com/cli/cli/releases/download/v${GHVER}/gh_${GHVER}_linux_${GHARCH}.tar.gz"; \
    tar -xzf /tmp/gh.tgz -C /tmp; \
    mv /tmp/gh_${GHVER}_linux_${GHARCH}/bin/gh /opt/agent/bin/gh

# tower: opt-in, prebuilt linux tarball
RUN set -eux; \
    if [ "${WITH_TOWER}" = "1" ]; then \
      case "${TARGETARCH}" in amd64) TARCH=x86_64-unknown-linux-gnu ;; arm64) TARCH=aarch64-unknown-linux-gnu ;; esac; \
      curl -fsSL -o /tmp/tower.tgz "https://github.com/awf-project/tower/releases/download/${TOWER_VERSION}/tower-${TOWER_VERSION}-${TARCH}.tar.gz"; \
      tar -xzf /tmp/tower.tgz -C /tmp; \
      find /tmp -type f -name tower -perm -u+x -exec mv {} /opt/agent/bin/tower \; ; \
      chmod +x /opt/agent/bin/tower; \
    fi

# awf-gh: built on the host by `make build`, copied here
COPY bin/ /opt/agent/bin/
RUN chmod +x /opt/agent/bin/*

USER agent
WORKDIR /home/agent

# The official installer writes to ~/.opencode/bin, which stays in the image.
RUN curl -fsSL https://opencode.ai/install | bash

# Pre-create every bind-mount target: Docker creates missing parents as root,
# and then the agent cannot write next to them.
RUN mkdir -p /home/agent/.local/share/opencode /home/agent/.local/state \
             /home/agent/.config/gh /home/agent/.config/opencode /state \
    && chown -R agent:agent /home/agent /state

ENTRYPOINT ["/usr/bin/tini", "--"]
CMD ["bash"]
