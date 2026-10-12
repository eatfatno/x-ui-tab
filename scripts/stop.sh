#!/usr/bin/env bash
set -euo pipefail
source "$(dirname "$0")/env.sh"
python3 "$PROJECT_ROOT/scripts/lab.py" stop "$@"
