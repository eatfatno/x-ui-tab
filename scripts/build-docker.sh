#!/usr/bin/env bash
set -euo pipefail
source "$(dirname "$0")/env.sh"
panel_version="${PANEL_VERSION:-3.10.2}"
monitor_version="${MONITOR_VERSION:-1.0.2}"
image="x-ui-tab:$panel_version-$monitor_version-amd64"
export DOCKER_CONFIG="$PROJECT_ROOT/cache/docker-client"
mkdir -p "$DOCKER_CONFIG" "$PROJECT_ROOT/artifacts/build/docker"
cd "$PROJECT_ROOT/apps/traffic-monitor/web"
npm ci --no-audit --no-fund
npm run build
cd "$PROJECT_ROOT/apps/traffic-monitor"
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -ldflags='-s -w' -o "$PROJECT_ROOT/artifacts/build/docker/traffic-monitor" ./cmd/monitor
cd "$PROJECT_ROOT"
python3 scripts/prepare-docker.py --panel-version "$panel_version" --monitor-version "$monitor_version"
# Caller selects the isolated project engine using docker-engine.sh env.
DOCKER_BUILDKIT=0 docker build --network=host --pull=false -t "$image" artifacts/docker/context
python3 scripts/package-docker.py
