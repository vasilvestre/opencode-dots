#!/usr/bin/env bash
# Runs the deploy scripts in a Debian 13 container (ADR 0028). It needs docker and the
# network, no KVM and no root on the host. The user step clones the committed state of
# this repository, so commit the unit files before you run it.
#
#   deploy/test/run.sh
set -euo pipefail
cd "$(dirname "$0")/../.."
command -v docker >/dev/null 2>&1 || { echo "deploy test: docker is not installed" >&2; exit 1; }
for f in deploy/lib.sh deploy/types/local/system.sh deploy/types/local/user.sh deploy/types/local/push.sh deploy/types/local/mcp.sh deploy/types/local/skills.sh deploy/test/inside.sh deploy/test/offline.sh; do
  bash -n "$f"
done
exec docker run --rm -v "$PWD":/src:ro debian:13 bash /src/deploy/test/inside.sh
