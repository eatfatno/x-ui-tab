#!/usr/bin/env bash
set -euo pipefail
source "$(dirname "$0")/env.sh"
cd "$PROJECT_ROOT/apps/traffic-monitor"
PROFILE_CPU=$(python3 -c 'import os; print(min(os.sched_getaffinity(0)))')
PROFILE_REPORT="$PROJECT_ROOT/artifacts/reports/resource-peak-$(date -u +%Y%m%dT%H%M%S)-$$.json"
go test -c -o "$PROJECT_ROOT/artifacts/build/resource-peak.test" ./internal/monitor
MONITOR_PEAK_OUTPUT="$PROFILE_REPORT" GOMAXPROCS=1 GOMEMLIMIT=192MiB \
  taskset -c "$PROFILE_CPU" "$PROJECT_ROOT/artifacts/build/resource-peak.test" \
  -test.run '^TestResourceConcurrentPeak$' -test.v -test.timeout 8m
