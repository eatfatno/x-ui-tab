#!/usr/bin/env python3
"""Download pinned official artifacts only into this workspace; verify release hashes."""
import hashlib
import json
from pathlib import Path
import urllib.request

ROOT = Path(__file__).resolve().parents[1]
DEST = ROOT / 'upstream' / '3x-ui'

def fetch(url, path):
    path.parent.mkdir(parents=True, exist_ok=True)
    if path.exists():
        return path
    print('Downloading', url, flush=True)
    req = urllib.request.Request(url, headers={'User-Agent': 'x-ui-tab-development'})
    part = path.with_suffix(path.suffix + '.part')
    with urllib.request.urlopen(req, timeout=120) as response, part.open('wb') as out:
        while chunk := response.read(1024 * 1024):
            out.write(chunk)
    part.replace(path)
    return path

def verify(path, expected):
    actual = hashlib.file_digest(path.open('rb'), 'sha256').hexdigest()
    if actual != expected:
        raise RuntimeError(f'Checksum mismatch: {path.name}')
    return actual

def main():
    # Builds consume the verified official binary, not a full source archive.
    # prepare-docker.py fetches only the required plain-text config and license.
    version = 'v3.10.2'
    go_name = 'go1.27.1.linux-amd64.tar.gz'
    go_file = fetch('https://go.dev/dl/' + go_name, ROOT / 'tools' / 'downloads' / go_name)
    go_sha = verify(go_file, '63d339f0da5ab53635a56f2490a7984dfe12dfcff22ad749f63edaf590168445')
    print('Go archive verified; extract using WSL tar to preserve permissions.', flush=True)
    base = 'https://github.com/MHSanaei/3x-ui/releases/download/' + version + '/'
    release = fetch(base + 'x-ui-linux-amd64.tar.gz', DEST / version / 'x-ui-linux-amd64.tar.gz')
    checksum = fetch(base + 'x-ui-linux-amd64.tar.gz.sha256', release.with_suffix('.gz.sha256'))
    release_sha = verify(release, checksum.read_text().split()[0])
    verify(release, 'bfda7921984e7a72555c38d4e861c7a8b67b3686b658608937de99682f1452d0')
    (DEST / 'manifest.json').write_text(json.dumps({'panel_version': version, 'release_sha256': release_sha, 'go_version': '1.27.1', 'go_sha256': go_sha}, indent=2) + '\n')
    print('Official artifacts downloaded and verified.', flush=True)

if __name__ == '__main__':
    main()
