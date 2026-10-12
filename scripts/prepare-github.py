#!/usr/bin/env python3
"""Create a reviewable source-only snapshot for GitHub; never publishes anything."""
import hashlib
import json
import os
from pathlib import Path
import re
import shutil
import zipfile

ROOT = Path(__file__).resolve().parents[1]
OUT = ROOT / 'artifacts/github'
DEST = OUT / 'source'
ENTRIES = ('apps/traffic-monitor', 'deploy/docker', 'docs', 'scripts', 'README.md',
           'LICENSE', 'THIRD_PARTY_NOTICES.md', '.gitignore', '.gitattributes')
EXCLUDED = {'node_modules', 'dist', '__pycache__', '.cache', '.git', 'data', 'runtime', 'cache', 'tools', 'backups', 'upstream', 'artifacts'}
SECRET_KEYS = {'password', 'passwd', 'token', 'api_token', 'apikey', 'privatekey', 'private_key', 'uuid'}


def private_values():
    values = set()
    def walk(value):
        if isinstance(value, dict):
            for key, item in value.items():
                if key.lower() in SECRET_KEYS and isinstance(item, str) and len(item) >= 12:
                    values.add(item.encode())
                walk(item)
        elif isinstance(value, list):
            for item in value:
                walk(item)
    runtime = ROOT / 'runtime'
    for directory, dirs, names in os.walk(runtime):
        dirs[:] = [d for d in dirs if d not in ('logs', 'node_modules', '.cache')]
        for name in names:
            path = Path(directory) / name
            if path.suffix == '.json' and path.stat().st_size < 2 * 1024**2:
                try:
                    walk(json.loads(path.read_text()))
                except (UnicodeError, ValueError):
                    pass
            elif name == 'api-token':
                value = path.read_bytes().strip()
                if len(value) >= 12:
                    values.add(value)
    return values


def candidates():
    for entry in ENTRIES:
        path = ROOT / entry
        if path.is_file():
            yield path
        else:
            for directory, dirs, names in os.walk(path):
                dirs[:] = [d for d in dirs if d not in EXCLUDED]
                for name in names:
                    if name == '.env' or name.startswith('.env.') or name.endswith(('.log', '.db', '.db-wal', '.db-shm', '.db-journal', '.pyc', '.key', '.pem')):
                        continue
                    yield Path(directory) / name


def main():
    if (DEST / '.git').exists():
        raise RuntimeError('The source snapshot is already a Git repository; preserve its work and use normal Git updates')
    values = private_values()
    patterns = [rb'(?:vless|vmess|trojan)://[^\s<>"\x27]+',
                rb'-----BEGIN (?:RSA |EC |OPENSSH )?PRIVATE KEY-----',
                rb'(?:ghp_|github_pat_)[A-Za-z0-9_]{20,}']
    selected, findings = [], []
    for path in sorted(candidates()):
        relative = path.relative_to(ROOT).as_posix()
        if path.is_symlink():
            raise ValueError('Source symlink is not publishable: ' + relative)
        data = path.read_bytes()
        if len(data) > 25 * 1024**2:
            raise ValueError('Unexpectedly large source file: ' + relative)
        if any(value in data for value in values) or any(re.search(pattern, data) for pattern in patterns):
            findings.append(relative)
        selected.append((path, relative, data))
    if findings:
        raise RuntimeError('Review potential private content in: ' + ', '.join(findings))
    # DEST is a fixed project child; no user-derived recursive removal path.
    if DEST.exists():
        shutil.rmtree(DEST)
    DEST.mkdir(parents=True)
    inventory = []
    with zipfile.ZipFile(OUT / 'x-ui-tab-github-source.zip', 'w', compression=zipfile.ZIP_DEFLATED) as archive:
        for path, relative, data in selected:
            target = DEST / relative
            target.parent.mkdir(parents=True, exist_ok=True)
            shutil.copy2(path, target)
            if path.suffix in ('.sh', '.py') or relative == 'deploy/docker/x-ui':
                target.chmod(0o755)
            archive.write(target, relative)
            inventory.append({'path': relative, 'bytes': len(data), 'sha256': hashlib.sha256(data).hexdigest()})
    (OUT / 'source-manifest.json').write_text(json.dumps({'repository': 'eatfatno/x-ui-tab',
        'file_count': len(inventory), 'bytes': sum(x['bytes'] for x in inventory),
        'private_pattern_check': 'passed', 'local_private_value_check': 'passed',
        'files': inventory}, indent=2) + '\n')
    print('Prepared source files:', len(inventory))
    print('Review directory:', DEST)
    print('No GitHub upload was performed.')


if __name__ == '__main__':
    main()
