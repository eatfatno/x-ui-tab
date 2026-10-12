#!/usr/bin/env bash
set -euo pipefail
source "$(dirname "$0")/env.sh"
cd "$PROJECT_ROOT/apps/traffic-monitor"
PROFILE_CPU=$(python3 -c 'import os; print(min(os.sched_getaffinity(0)))')
PROFILE_REPORT="$PROJECT_ROOT/artifacts/reports/resource-$(date -u +%Y%m%dT%H%M%S)-$$.json"
mkdir -p "$PROJECT_ROOT/artifacts/reports" "$PROJECT_ROOT/artifacts/build"
go test -c -o "$PROJECT_ROOT/artifacts/build/resource-profile.test" ./internal/monitor
MONITOR_PROFILE_OUTPUT="$PROFILE_REPORT" GOMAXPROCS=1 GOMEMLIMIT=192MiB \
  taskset -c "$PROFILE_CPU" "$PROJECT_ROOT/artifacts/build/resource-profile.test" \
  -test.run '^TestResourceProfile$' -test.v -test.timeout 8m
MONITOR_PROFILE_FIXTURE="$PROFILE_REPORT" GOMAXPROCS=1 GOMEMLIMIT=192MiB \
  taskset -c "$PROFILE_CPU" "$PROJECT_ROOT/artifacts/build/resource-profile.test" \
  -test.run '^TestResourceRecentWindows$' -test.v -test.timeout 2m
printf 'Reports and isolated synthetic database: %s\n' "$PROFILE_REPORT"
