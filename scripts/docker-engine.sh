#!/usr/bin/env bash
# Isolated WSL build daemon. Its disk image, sockets, logs and caches stay here.
set -euo pipefail
source "$(dirname "$0")/env.sh"
engine="$PROJECT_ROOT/cache/docker-engine"
mount_dir="$engine/fs"
socket="$mount_dir/docker.sock"
export DOCKER_CONFIG="$PROJECT_ROOT/cache/docker-client"
mkdir -p "$engine" "$mount_dir" "$(dirname "$socket")" "$DOCKER_CONFIG"
case "${1:-status}" in
  start)
    if DOCKER_HOST="unix://$socket" docker info >/dev/null 2>&1; then exit 0; fi
    if ! mountpoint -q "$mount_dir"; then
      if [[ ! -f "$engine/storage.ext4" ]]; then
        truncate -s 8G "$engine/storage.ext4"
        mkfs.ext4 -q -F "$engine/storage.ext4"
      fi
      sudo mount -o loop "$engine/storage.ext4" "$mount_dir"
      sudo chown "$(id -u):$(id -g)" "$mount_dir"
    fi
    mkdir -p "$mount_dir/tmp" "$mount_dir/run"
    printf '{}\n' > "$engine/daemon.json"
    sudo env TMPDIR="$mount_dir/tmp" DOCKER_TMPDIR="$mount_dir/tmp" \
      sh -c 'nohup dockerd --config-file "$1/daemon.json" --data-root "$2/data" --exec-root "$2/run" --pidfile "$1/docker.pid" --host "unix://$3" --storage-driver overlay2 --feature containerd-snapshotter=false --bridge none --iptables=false --ip6tables=false --ip-forward=false --ip-masq=false > "$1/daemon.log" 2>&1 < /dev/null &' sh "$engine" "$mount_dir" "$socket"
    for _ in $(seq 1 60); do
      if DOCKER_HOST="unix://$socket" docker info >/dev/null 2>&1; then
        echo "Project Docker engine ready: $socket"
        exit 0
      fi
      sleep 1
    done
    echo "Docker start failed; inspect $engine/daemon.log" >&2; exit 1
    ;;
  stop)
    if [[ -f "$engine/docker.pid" ]]; then
      pid=$(cat "$engine/docker.pid")
      if [[ -r "/proc/$pid/cmdline" ]] && tr '\0' '\n' < "/proc/$pid/cmdline" | grep -Fxq "$engine/daemon.json"; then
        if [[ -n "$(DOCKER_HOST="unix://$socket" docker ps -q)" ]]; then
          echo 'Stop project containers before stopping the build engine.' >&2; exit 1
        fi
        sudo kill -TERM "$pid"
        for _ in $(seq 1 30); do [[ ! -e "/proc/$pid/exe" ]] && break; sleep 1; done
      fi
    fi
    # Docker leaves its private data bind mount and default netns mount behind.
    # All three paths are fixed descendants of the project work disk.
    for target in "$mount_dir/run/netns/default" "$mount_dir/data" "$mount_dir"; do
      if mountpoint -q "$target"; then sudo umount "$target"; fi
    done
    ;;
  env)
    printf 'export DOCKER_HOST=%q\nexport DOCKER_CONFIG=%q\n' "unix://$socket" "$DOCKER_CONFIG"
    ;;
  status) DOCKER_HOST="unix://$socket" docker info --format '{{.DockerRootDir}} {{.Driver}}' ;;
  *) echo 'Usage: docker-engine.sh start|stop|env|status' >&2; exit 2 ;;
esac
