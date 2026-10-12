#!/usr/bin/env python3
"""Download pinned official artifacts only into this workspace; verify release hashes."""
import hashlib
import json
from pathlib import Path
import tarfile
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
    source = fetch('https://codeload.github.com/MHSanaei/3x-ui/tar.gz/refs/tags/v3.9.0', DEST / '3x-ui-v3.9.0-source.tar.gz')
    source_dir = DEST / 'source'
    if not source_dir.exists():
        source_dir.mkdir()
        with tarfile.open(source) as archive:
            for member in archive.getmembers():
                parts = Path(member.name).parts
                if len(parts) < 2:
                    continue
                member.name = '/'.join(parts[1:])
                archive.extract(member, source_dir, filter='data')
    print('Official source ready:', source_dir, flush=True)
    go_name = 'go1.27.1.linux-amd64.tar.gz'
    go_file = fetch('https://go.dev/dl/' + go_name, ROOT / 'tools' / 'downloads' / go_name)
    go_sha = verify(go_file, '63d339f0da5ab53635a56f2490a7984dfe12dfcff22ad749f63edaf590168445')
    print('Go archive verified; extract using WSL tar to preserve permissions.', flush=True)
    base = 'https://github.com/MHSanaei/3x-ui/releases/download/v3.9.0/'
    release = fetch(base + 'x-ui-linux-amd64.tar.gz', DEST / 'v3.9.0' / 'x-ui-linux-amd64.tar.gz')
    checksum = fetch(base + 'x-ui-linux-amd64.tar.gz.sha256', release.with_suffix('.gz.sha256'))
    release_sha = verify(release, checksum.read_text().split()[0])
    (DEST / 'manifest.json').write_text(json.dumps({'panel_version': 'v3.9.0', 'release_sha256': release_sha, 'go_version': '1.27.1', 'go_sha256': go_sha}, indent=2) + '\n')
    print('Official artifacts downloaded and verified.', flush=True)

if __name__ == '__main__':
    main()
