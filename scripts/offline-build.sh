#!/usr/bin/env bash
# Proves the "clean container, no outbound network" rule for a given stage.
set -euo pipefail
STAGE="${1:-stage-1}"
docker build --network=none --build-arg STAGE="$STAGE" -f deploy/Dockerfile -t obsidian-foundry:"$STAGE" .
