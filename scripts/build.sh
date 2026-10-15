#!/usr/bin/env bash
set -euo pipefail
source "$(dirname "$0")/env.sh"
cd "$PROJECT_ROOT/apps/traffic-monitor/web"
npm ci --no-audit --no-fund
npm run build
mkdir -p "$PROJECT_ROOT/runtime/monitor/www" "$PROJECT_ROOT/artifacts/build"
cp -a dist/. "$PROJECT_ROOT/runtime/monitor/www/"
cd "$PROJECT_ROOT/apps/traffic-monitor"
go build -trimpath -o "$PROJECT_ROOT/artifacts/build/traffic-monitor" ./cmd/monitor
cp "$PROJECT_ROOT/artifacts/build/traffic-monitor" "$PROJECT_ROOT/runtime/monitor/traffic-monitor.new"
chmod +x "$PROJECT_ROOT/runtime/monitor/traffic-monitor.new"
mv "$PROJECT_ROOT/runtime/monitor/traffic-monitor.new" "$PROJECT_ROOT/runtime/monitor/traffic-monitor"
echo 'Built monitor and frontend. Restart the monitor to activate the new binary.'
