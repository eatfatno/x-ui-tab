#!/usr/bin/env python3
"""Stage an explicit allowlist. No runtime credentials, databases or histories."""
import argparse
import hashlib
import json
from pathlib import Path
import re
import shutil
import tarfile
import urllib.request

ROOT = Path(__file__).resolve().parents[1]


def digest(path):
    with path.open('rb') as f:
        return hashlib.file_digest(f, 'sha256').hexdigest()


def fetch(url, path):
    if not path.exists():
        path.parent.mkdir(parents=True, exist_ok=True)
        with urllib.request.urlopen(url, timeout=90) as r, path.with_suffix('.part').open('wb') as f:
            shutil.copyfileobj(r, f)
        path.with_suffix('.part').replace(path)


def main():
    p = argparse.ArgumentParser()
    p.add_argument('--panel-version', default='3.10.2')
    p.add_argument('--monitor-version', default='1.0.2')
    args = p.parse_args()
    for v in (args.panel_version, args.monitor_version):
        if not re.fullmatch(r'\d+\.\d+\.\d+', v):
            raise ValueError('Expected a stable X.Y.Z version')
    release = ROOT / 'upstream/3x-ui' / ('v' + args.panel_version)
    archive = release / 'x-ui-linux-amd64.tar.gz'
    checksum = release / (archive.name + '.sha256')
    base = 'https://github.com/MHSanaei/3x-ui/releases/download/v' + args.panel_version + '/'
    fetch(base + archive.name, archive)
    fetch(base + checksum.name, checksum)
    sha = digest(archive)
    if sha != checksum.read_text().split()[0]:
        raise RuntimeError('Official archive SHA256 mismatch')
    context = ROOT / 'artifacts/docker/context'
    if context.exists():
        shutil.rmtree(context)
    context.mkdir(parents=True)
    with tarfile.open(archive) as tar:
        for m in tar.getmembers():
            parts = Path(m.name).parts
            if len(parts) < 2 or parts[0] != 'x-ui' or parts[1] not in ('x-ui', 'bin'):
                continue
            m.name = '/'.join(('panel', *parts[1:]))
            tar.extract(m, context, filter='data')
    for name in ('Dockerfile', '.dockerignore', 'container.py'):
        shutil.copy2(ROOT / 'deploy/docker' / name, context / name)
    shutil.copy2(ROOT / 'artifacts/build/docker/traffic-monitor', context / 'traffic-monitor')
    shutil.copytree(ROOT / 'apps/traffic-monitor/web/dist', context / 'www')
    licenses = context / 'licenses'
    licenses.mkdir()
    # Do not restore or redistribute the source tarball quarantined by Windows
    # Defender. These two plain-text inputs are the only source files needed.
    texts = release / 'build-inputs'
    for relative, dest in [('internal/web/service/config.json', context / 'default-xray.json'), ('LICENSE', licenses / '3x-ui-LICENSE')]:
        local = texts / Path(relative).name
        if args.panel_version == '3.9.0' and (ROOT / 'upstream/3x-ui/source' / relative).is_file():
            local.parent.mkdir(parents=True, exist_ok=True)
            shutil.copy2(ROOT / 'upstream/3x-ui/source' / relative, local)
        else:
            fetch('https://raw.githubusercontent.com/MHSanaei/3x-ui/v' + args.panel_version + '/' + relative, local)
        shutil.copy2(local, dest)
    json.loads((context / 'default-xray.json').read_text())
    for path in (ROOT / 'apps/traffic-monitor/internal/monitor/rules').rglob('LICENSE*'):
        shutil.copy2(path, licenses / path.name)
    manifest = dict(panel_version=args.panel_version, monitor_version=args.monitor_version,
                    architecture='amd64', panel_sha256=sha,
                    panel_source_url='https://github.com/MHSanaei/3x-ui/tree/v' + args.panel_version,
                    default_template_sha256=digest(context / 'default-xray.json'),
                    monitor_sha256=digest(context / 'traffic-monitor'),
                    image='x-ui-tab:' + args.panel_version + '-' + args.monitor_version + '-amd64')
    (context / 'manifest.json').write_text(json.dumps(manifest, indent=2) + '\n')
    print('Staged allowlisted context:', context)


if __name__ == '__main__':
    main()
