#!/usr/bin/env bash
# Remove only the legacy deployment reviewed on 2026-10-08. No backup/migration.
# Default is a read-only dry run. Use --execute to carry out the authorized cleanup.
set -euo pipefail
mode="${1:---dry-run}"
[[ "$mode" == --dry-run || "$mode" == --execute ]] || { echo 'Usage: cleanup-legacy.sh [--dry-run|--execute]' >&2; exit 2; }
[[ $EUID -eq 0 ]] || { echo 'Run this script as root inside Ubuntu-24.04 WSL.' >&2; exit 1; }
project_root=/mnt/d/work/codexworkspace/x-ui-tab
[[ "$(realpath -e "$(dirname "${BASH_SOURCE[0]}")/..")" == "$project_root" ]] || { echo 'Unexpected project root.' >&2; exit 1; }
export TMPDIR="$project_root/cache/tmp"

directories=(/home/john/x-ui /etc/x-ui /var/log/x-ui /opt/3x-ui-custom /home/john/.cache/3x-ui-traffic-monitor)
wrappers=(/usr/bin/x-ui /usr/local/bin/x-ui)
units=(/etc/systemd/system/x-ui-custom.service /etc/systemd/system/x-ui.service /etc/systemd/system/multi-user.target.wants/x-ui-custom.service)
legacy_container=1e4fb806f36e
legacy_container_name=traffic-monitor-systemd-test-3f50465b00ab
legacy_image=traffic-monitor-systemd-test:local
legacy_image_id=sha256:2120668ad718

fail() { printf 'REFUSED: %s\n' "$*" >&2; exit 1; }
for p in "${directories[@]}"; do
  [[ -e "$p" || -L "$p" ]] || continue
  [[ ! -L "$p" && -d "$p" ]] || fail "$p is no longer an ordinary directory"
  [[ "$(realpath -e "$p")" == "$p" ]] || fail "$p resolves outside the exact reviewed path"
  mountpoint -q "$p" && fail "$p is a mount point"
  printf 'Reviewed directory: %s\n' "$p"
done
if [[ -d /home/john/x-ui ]]; then
  [[ -f /home/john/x-ui/start.sh && -f /home/john/x-ui/x-ui ]] || fail 'Legacy deployment identity changed'
fi
if [[ -d /opt/3x-ui-custom ]]; then
  grep -q '^module github.com/mhsanaei/3x-ui/v3' /opt/3x-ui-custom/go.mod || fail 'Legacy source identity changed'
fi
for p in "${wrappers[@]}"; do
  [[ -e "$p" || -L "$p" ]] || continue
  [[ -f "$p" && ! -L "$p" ]] || fail "$p changed type"
  grep -q 'XUI_MAIN_FOLDER="/home/john/x-ui"' "$p" || fail "$p no longer references the legacy deployment"
  grep -q 'XUI_SERVICE_NAME="x-ui-custom"' "$p" || fail "$p no longer references the legacy service"
done
if [[ -e "${units[0]}" ]]; then
  [[ ! -L "${units[0]}" ]] || fail 'Legacy primary service is now a symlink'
  grep -q '^ExecStart=/home/john/x-ui/start.sh' "${units[0]}" || fail 'Legacy service identity changed'
fi
for p in "${units[@]:1}"; do
  [[ -e "$p" || -L "$p" ]] || continue
  [[ -L "$p" && "$(readlink "$p")" == /etc/systemd/system/x-ui-custom.service ]] || fail "$p is no longer the reviewed alias"
done

container_present=false
image_present=false
if command -v docker >/dev/null && docker info >/dev/null 2>&1; then
  if docker inspect "$legacy_container" >/dev/null 2>&1; then
    [[ "$(docker inspect "$legacy_container" --format '{{.Name}}')" == "/$legacy_container_name" ]] || fail 'Container identity changed'
    [[ "$(docker inspect "$legacy_container" --format '{{.Config.Image}}')" == "$legacy_image" ]] || fail 'Container image changed'
    [[ "$(docker inspect "$legacy_container" --format '{{len .Mounts}}')" == 0 ]] || fail 'Container gained external mounts'
    container_present=true
  fi
  if docker image inspect "$legacy_image" >/dev/null 2>&1; then
    image_id="$(docker image inspect "$legacy_image" --format '{{.Id}}')"
    [[ "$image_id" == "$legacy_image_id"* ]] || fail 'Legacy image tag now points to a different image'
    image_present=true
  fi
fi
printf 'Mode: %s; legacy container present: %s; legacy image present: %s\n' "$mode" "$container_present" "$image_present"
if [[ "$mode" == --dry-run ]]; then
  printf '%s\n' 'Validated the fixed removal list. No files, services, containers or images changed.'
  exit 0
fi

if [[ -e "${units[0]}" ]]; then
  systemctl stop x-ui-custom.service
  systemctl disable x-ui-custom.service
fi
for proc in /proc/[0-9]*/exe; do
  target="$(readlink "$proc" 2>/dev/null || true)"
  case "$target" in
    /home/john/x-ui/x-ui|/home/john/x-ui/bin/xray-linux-amd64)
      fail "Legacy process still alive: $proc -> $target; no directories have been removed" ;;
  esac
done
rm -f -- "${units[@]}" "${wrappers[@]}"
systemctl daemon-reload
systemctl reset-failed x-ui-custom.service x-ui.service 2>/dev/null || true
for p in "${directories[@]}"; do
  # Exact literal allowlist and realpath checks above; do not cross nested mounts.
  rm -rf --one-file-system -- "$p"
done
if [[ "$container_present" == true ]]; then docker rm "$legacy_container"; fi
if [[ "$image_present" == true ]]; then docker image rm "$legacy_image"; fi

for p in "${directories[@]}" "${wrappers[@]}" "${units[@]}"; do
  [[ ! -e "$p" && ! -L "$p" ]] || fail "Remnant exists: $p"
done
[[ "$(systemctl show x-ui-custom.service -p LoadState --value)" == not-found ]] || fail 'Legacy service is still installed'
[[ "$(systemctl show x-ui.service -p LoadState --value)" == not-found ]] || fail 'Legacy alias is still installed'
printf '%s\n' 'Legacy cleanup completed; unrelated containers, images and system components were preserved.'
