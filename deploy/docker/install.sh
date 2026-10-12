#!/usr/bin/env bash
set -euo pipefail
package_dir="$(cd "$(dirname "$0")" && pwd)"
bin_dir="${1:-/usr/local/bin}"
command -v python3 >/dev/null || { echo 'Please install Python 3.10+.' >&2; exit 1; }
python3 -c 'import sys; assert sys.version_info >= (3,10), "Python 3.10+ is required"'
docker compose version >/dev/null
mkdir -p "$bin_dir"
chmod +x "$package_dir/x-ui"
target="$bin_dir/x-ui"
existing_command="$(command -v x-ui || true)"
if [[ "$bin_dir" == /usr/local/bin && -n "$existing_command" ]] && [[ "$(readlink -f "$existing_command")" != "$package_dir/x-ui" ]]; then
  echo "Existing $existing_command belongs to another installation. Using x-ui-tab."
  target="$bin_dir/x-ui-tab"
fi
if [[ -e "$target" || -L "$target" ]]; then
  if [[ "$(readlink -f "$target")" != "$package_dir/x-ui" ]]; then
    echo "Existing $target belongs to another installation. Kept it intact."
    target="$bin_dir/x-ui-tab"
    if [[ -e "$target" || -L "$target" ]] && [[ "$(readlink -f "$target")" != "$package_dir/x-ui" ]]; then
      echo "Existing $target also belongs to another installation." >&2; exit 1
    fi
  fi
fi
ln -sfn "$package_dir/x-ui" "$target"
echo "Management command installed: $target"
echo "Next: $target load && $target init"
echo 'Run the command without arguments for the interactive menu.'
