#!/usr/bin/env bash
# Source from any working directory. Never overrides HOME.
export PROJECT_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
export TMPDIR="$PROJECT_ROOT/cache/tmp"
export TMP="$TMPDIR"
export TEMP="$TMPDIR"
export GOCACHE="$PROJECT_ROOT/cache/go-build"
export GOMODCACHE="$PROJECT_ROOT/cache/go-mod"
export GOPATH="$PROJECT_ROOT/cache/go"
export GOTOOLCHAIN=local
export GOPROXY="${GOPROXY:-https://goproxy.cn,https://proxy.golang.org,direct}"
export npm_config_cache="$PROJECT_ROOT/cache/npm"
export XDG_CACHE_HOME="$PROJECT_ROOT/cache/xdg"
export XDG_CONFIG_HOME="$PROJECT_ROOT/cache/xdg-config"
export XDG_DATA_HOME="$PROJECT_ROOT/cache/xdg-data"
export PLAYWRIGHT_BROWSERS_PATH="$PROJECT_ROOT/cache/playwright"
export PYTHONDONTWRITEBYTECODE=1
export PIP_CACHE_DIR="$PROJECT_ROOT/cache/pip"
export PATH="$PROJECT_ROOT/tools/go/bin:$PATH"
mkdir -p "$TMPDIR" "$GOCACHE" "$GOMODCACHE" "$npm_config_cache" "$XDG_CACHE_HOME" "$XDG_CONFIG_HOME" "$XDG_DATA_HOME" "$PLAYWRIGHT_BROWSERS_PATH"
