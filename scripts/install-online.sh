#!/usr/bin/env bash
# GitHub release installer. Source repository: https://github.com/eatfatno/x-ui-tab
# Parse the whole installer before any child process can read a curl | bash pipe.
main() {
set -euo pipefail
umask 077

repo=eatfatno/x-ui-tab
version=v1.0.1
install_dir=/opt/x-ui-tab
bin_dir=/usr/local/bin
install_deps=1
port=18080
bind=0.0.0.0
base_path=/
panel_port=12053
api_port=62789
metrics_port=11111
project_name=x-ui-tab

usage() {
  cat <<'HELP'
Install the tested 3x-ui + traffic monitor GitHub release (Linux amd64).
  --version v1.0.1             Exact published release tag (default: v1.0.1)
  --repo eatfatno/x-ui-tab     GitHub repository
  --dir /opt/x-ui-tab          Persistent deployment directory
  --bin-dir /usr/local/bin    Management command directory
  --port 18080                Public panel port
  --bind 0.0.0.0              Public listen address
  --base-path /               Panel path
  --panel-port 12053          Internal panel port
  --api-port 62789            Internal Xray API port
  --metrics-port 11111        Internal metrics port
  --project-name x-ui-tab     Unique Compose project name
  --no-install-deps           Require preinstalled Docker, Compose v2, Python 3.10+
  --help

Fresh automatic dependency installation supports Ubuntu 22.04/24.04.
Other Linux distributions can use --no-install-deps after installing dependencies.
Existing deployments are preserved; image updates use the x-ui management command.
HELP
}
die() { printf 'Error: %s\n' "$*" >&2; exit 1; }
while (($#)); do
  case "$1" in
    --help|-h) usage; exit 0 ;;
    --no-install-deps) install_deps=0; shift ;;
    --repo|--version|--dir|--bin-dir|--port|--bind|--base-path|--panel-port|--api-port|--metrics-port|--project-name)
      (($# >= 2)) || die "Missing value for $1"
      case "$1" in
        --repo) repo=$2 ;; --version) version=$2 ;; --dir) install_dir=$2 ;;
        --bin-dir) bin_dir=$2 ;; --port) port=$2 ;; --bind) bind=$2 ;;
        --base-path) base_path=$2 ;; --panel-port) panel_port=$2 ;;
        --api-port) api_port=$2 ;; --metrics-port) metrics_port=$2 ;;
        --project-name) project_name=$2 ;;
      esac
      shift 2 ;;
    *) die "Unknown argument: $1" ;;
  esac
done

[[ $repo =~ ^[A-Za-z0-9-]+/[A-Za-z0-9_.-]+$ ]] || die 'Invalid GitHub repository'
[[ $version =~ ^v[0-9]+\.[0-9]+\.[0-9]+$ ]] || die 'Use an exact stable tag such as v1.0.1'
[[ $project_name =~ ^[a-z0-9][a-z0-9_-]*$ ]] || die 'Invalid Compose project name'
[[ $install_dir == /* && $bin_dir == /* ]] || die 'Installation paths must be absolute'
[[ $install_dir != *$'\n'* && $bin_dir != *$'\n'* ]] || die 'Invalid installation path'
for value in "$port" "$panel_port" "$api_port" "$metrics_port"; do
  [[ $value =~ ^[1-9][0-9]{0,4}$ ]] && ((value <= 65535)) || die 'Ports must be between 1 and 65535'
done
[[ $(uname -s) == Linux && $(uname -m) == x86_64 ]] || die 'This release supports Linux x86_64 / amd64 only'
((EUID == 0)) || die 'Run as root, or pipe the installer into sudo bash'

install_dir=$(realpath -m -- "$install_dir")
case "$install_dir" in /|/opt|/usr|/usr/local|/etc|/var|/home|/root) die 'Choose a dedicated deployment subdirectory' ;; esac
mkdir -p -- "$install_dir/.cache/online-install" "$install_dir/.cache/docker"
export TMPDIR="$install_dir/.cache/online-install"
export DOCKER_CONFIG="$install_dir/.cache/docker"
export PYTHONDONTWRITEBYTECODE=1
command -v flock >/dev/null || die 'Install util-linux (flock) first'
exec 9>"$install_dir/.cache/online-install.lock"
flock -n 9 || die 'Another online installation is running'

if [[ -e "$install_dir/data" || -e "$install_dir/.env" ]]; then
  die "An existing or partially initialized deployment exists in $install_dir. Use its x-ui status/start/update commands; do not reinstall over data."
fi

prepare_dependencies() {
  local python_ok=0 docker_ok=0 compose_ok=0
  if command -v python3 >/dev/null && python3 -c 'import sys; sys.exit(sys.version_info < (3,10))'; then python_ok=1; fi
  if command -v docker >/dev/null; then docker_ok=1; fi
  if ((docker_ok)) && docker compose version >/dev/null 2>&1; then compose_ok=1; fi
  if ((python_ok && docker_ok && compose_ok)) && command -v curl >/dev/null; then return; fi
  ((install_deps)) || die 'Requires Python 3.10+, curl, Docker Engine and Compose v2'
  [[ -f /etc/os-release ]] || die 'Unknown Linux distribution; install dependencies manually'
  # This is a system-owned file on the target host.
  . /etc/os-release
  [[ ${ID:-} == ubuntu && (${VERSION_ID:-} == 22.04 || ${VERSION_ID:-} == 24.04) ]] || die 'Automatic dependency installation supports Ubuntu 22.04/24.04; otherwise use --no-install-deps'
  export DEBIAN_FRONTEND=noninteractive
  apt-get update
  apt-get install -y --no-remove ca-certificates curl python3
  if ((!docker_ok || !compose_ok)); then
    if ((!docker_ok)); then
      # Do not silently replace an existing container runtime.
      if command -v containerd >/dev/null || command -v podman >/dev/null; then
        die 'Existing container runtime detected. Install Docker and Compose manually, then retry.'
      fi
    fi
    mkdir -p /etc/apt/keyrings
    if [[ ! -f /etc/apt/keyrings/docker.asc ]]; then
      curl --fail --show-error --silent --location --proto '=https' --proto-redir '=https' \
        https://download.docker.com/linux/ubuntu/gpg -o "$TMPDIR/docker.asc"
      install -m 0644 "$TMPDIR/docker.asc" /etc/apt/keyrings/docker.asc
    fi
    if ! apt-cache policy docker-compose-plugin | grep -q 'Candidate: [0-9]'; then
      [[ ! -e /etc/apt/sources.list.d/x-ui-tab-docker.list ]] || die 'Docker package source already exists but is not usable; check apt configuration'
      printf 'deb [arch=amd64 signed-by=/etc/apt/keyrings/docker.asc] https://download.docker.com/linux/ubuntu %s stable\n' "$VERSION_CODENAME" > /etc/apt/sources.list.d/x-ui-tab-docker.list
      apt-get update
    fi
    if ((docker_ok)); then
      apt-get install -y --no-remove docker-compose-plugin
    else
      apt-get install -y --no-remove docker-ce docker-ce-cli containerd.io docker-compose-plugin
      systemctl enable --now docker
    fi
  fi
  python3 -c 'import sys; assert sys.version_info >= (3,10), "Python 3.10+ is required"'
  docker compose version >/dev/null
}
prepare_dependencies
docker_endpoint=${DOCKER_HOST:-}
if [[ -n ${DOCKER_CONTEXT:-} || -z $docker_endpoint ]]; then
  docker_endpoint=$(docker context inspect "$(docker context show)" --format '{{.Endpoints.docker.Host}}')
fi
[[ $docker_endpoint == unix:///* ]] || die 'Use a local Docker daemon; remote contexts cannot bind this installation directory'
docker info >/dev/null || die 'Docker is not reachable; start the local Docker service and retry'

# Python validates both archive layers before any downloaded program is executed.
package_dir=$(python3 - "$repo" "$version" "$install_dir" <<'PY'
import hashlib
import json
from pathlib import Path, PurePosixPath
import re
import shutil
import sys
import tarfile
import tempfile
import urllib.request

repo, version, target = sys.argv[1:]
target = Path(target)
work = Path(tempfile.mkdtemp(prefix='release-', dir=target / '.cache/online-install'))

def fetch(url, path, limit):
    req = urllib.request.Request(url, headers={'User-Agent': 'x-ui-tab-installer', 'Accept': 'application/vnd.github+json'})
    with urllib.request.urlopen(req, timeout=90) as response, path.open('wb') as output:
        if not response.url.startswith('https://'):
            raise ValueError('Only HTTPS release downloads are supported')
        total = 0
        while chunk := response.read(1024 * 1024):
            total += len(chunk)
            if total > limit:
                raise ValueError('Release file exceeds its download limit')
            output.write(chunk)

def sha(path):
    value = hashlib.sha256()
    with path.open('rb') as f:
        for chunk in iter(lambda: f.read(1024 * 1024), b''):
            value.update(chunk)
    return value.hexdigest()

try:
    metadata = work / 'release.json'
    fetch('https://api.github.com/repos/' + repo + '/releases/tags/' + version, metadata, 2 * 1024**2)
    release = json.loads(metadata.read_text())
    if release.get('draft') or release.get('prerelease') or release.get('tag_name') != version:
        raise ValueError('Expected the requested published stable release')
    pattern = r'x-ui-tab-\d+\.\d+\.\d+-' + re.escape(version[1:]) + r'-linux-amd64\.tar\.gz'
    assets = [a for a in release.get('assets', []) if re.fullmatch(pattern, a.get('name', ''))]
    if len(assets) != 1:
        raise ValueError('Release must contain exactly one matching Linux amd64 deployment bundle')
    asset = assets[0]['name']
    names = {a.get('name') for a in release['assets']}
    if asset + '.sha256' not in names:
        raise ValueError('Release is missing its SHA-256 file')
    url = 'https://github.com/' + repo + '/releases/download/' + version + '/'
    checksum = work / (asset + '.sha256')
    fetch(url + checksum.name, checksum, 4096)
    line = checksum.read_text().strip()
    match = re.fullmatch(r'([0-9a-f]{64})\s+\*?' + re.escape(asset), line)
    if not match:
        raise ValueError('Invalid release checksum format')
    if shutil.disk_usage(target).free < 2 * 1024**3:
        raise ValueError('At least 2 GiB free space is required in the deployment filesystem')
    archive = work / asset
    print('Downloading ' + asset + ' ...', file=sys.stderr)
    fetch(url + asset, archive, 1024**3)
    if sha(archive) != match[1]:
        raise ValueError('Release checksum mismatch')
    expected_root = asset.removesuffix('.tar.gz')
    stage = work / 'unpacked'
    stage.mkdir()
    seen, total = set(), 0
    with tarfile.open(archive, 'r:gz') as tar:
        for member in tar.getmembers():
            path = PurePosixPath(member.name)
            parts = path.parts
            if (not parts or parts[0] != expected_root or path.is_absolute() or '..' in parts
                    or '\\' in member.name or member.name in seen or not (member.isdir() or member.isfile())):
                raise ValueError('Bundle contains an unsafe or repeated path')
            if len(parts) > 1 and parts[1] not in {'x-ui','install.sh','compose.yaml','bundle.json','image.tar.gz','SHA256SUMS','docs','licenses','sources','reports'}:
                raise ValueError('Bundle contains an unexpected top-level file')
            seen.add(member.name)
            total += member.size
            if total > 2 * 1024**3 or len(seen) > 10000:
                raise ValueError('Bundle exceeds extraction limits')
        if sys.version_info >= (3,12):
            tar.extractall(stage, filter='data')
        else:
            tar.extractall(stage)  # Only the validated directories and regular files above.
    package = stage / expected_root
    inventory = {}
    for line in (package / 'SHA256SUMS').read_text().splitlines():
        match = re.fullmatch(r'([0-9a-f]{64})  (.+)', line)
        if not match:
            raise ValueError('Invalid bundle inventory')
        checksum, name = match.groups()
        path = PurePosixPath(name)
        if name in inventory or path.is_absolute() or '..' in path.parts or '\\' in name:
            raise ValueError('Unsafe or duplicate inventory entry')
        inventory[name] = checksum
    actual = {p.relative_to(package).as_posix() for p in package.rglob('*') if p.is_file() and p.name != 'SHA256SUMS'}
    if actual != set(inventory):
        raise ValueError('Bundle inventory does not match extracted files')
    for name, expected in inventory.items():
        if sha(package / name) != expected:
            raise ValueError('Bundle content checksum mismatch: ' + name)
    required = {'x-ui','install.sh','compose.yaml','bundle.json','image.tar.gz'}
    if not required.issubset(inventory):
        raise ValueError('Incomplete deployment bundle')
    manifest = json.loads((package / 'bundle.json').read_text())
    if manifest.get('architecture') != 'amd64' or manifest.get('monitor_version') != version[1:]:
        raise ValueError('Bundle architecture or version does not match request')
    if manifest.get('image_archive_sha256') != inventory['image.tar.gz']:
        raise ValueError('Image archive does not match bundle metadata')
    # Failed fresh installs may be retried, but existing runtime data is never replaced.
    existing = [p for p in target.iterdir() if p.name != '.cache']
    marker = target / '.online-install.json'
    if existing:
        if not marker.is_file() or marker.is_symlink():
            raise ValueError('Deployment directory contains unrelated files; choose an empty directory')
        previous = json.loads(marker.read_text())
        if previous.get('repository') != repo or previous.get('version') != version:
            raise ValueError('Existing installation belongs to another repository or version')
    allowed = {'.cache', '.online-install.json', *[p.name for p in package.iterdir()]}
    if any(p.name not in allowed for p in target.iterdir()):
        raise ValueError('Deployment directory contains unrelated files')
    for child in package.iterdir():
        dest = target / child.name
        if dest.is_symlink():
            raise ValueError('Deployment contains a symbolic link: ' + child.name)
        if child.is_dir():
            if dest.exists():
                shutil.rmtree(dest)
            shutil.copytree(child, dest)
        else:
            shutil.copy2(child, dest)
    for name in ('x-ui', 'install.sh'):
        (target / name).chmod(0o755)
    (target / '.online-install.json').write_text(json.dumps({'repository':repo,'version':version,'archive':asset,'sha256':sha(archive)},indent=2)+'\n')
    print(target)
finally:
    shutil.rmtree(work)
PY
)

bash "$package_dir/install.sh" "$bin_dir"
python3 "$package_dir/x-ui" load
python3 "$package_dir/x-ui" init --bind "$bind" --port "$port" --base-path "$base_path" \
  --panel-port "$panel_port" --api-port "$api_port" --metrics-port "$metrics_port" --project-name "$project_name"
printf '\nInstallation complete. Management: %s/x-ui\n' "$package_dir"
printf 'Initial credentials: %s/data/state/credentials.json\n' "$package_dir"
}
main "$@" </dev/null
