#!/usr/bin/env python3
"""Replace official release artifacts while preserving independently stored monitor/data.

This workspace launcher deliberately does not call the global /usr/local installer.
"""
import argparse
import hashlib
import json
from pathlib import Path
import re
import shutil
import sys
import tarfile
import urllib.request

sys.path.insert(0, str(Path(__file__).parent))
import lab

def download(url, path):
    if path.exists():
        return
    path.parent.mkdir(parents=True, exist_ok=True)
    with urllib.request.urlopen(urllib.request.Request(url, headers={'User-Agent': 'x-ui-tab'}), timeout=120) as response:
        part = path.with_suffix(path.suffix + '.part')
        with part.open('wb') as out:
            shutil.copyfileobj(response, out)
        part.replace(path)

def release(version):
    if not re.fullmatch(r'v\d+\.\d+\.\d+', version):
        raise ValueError('Expected stable official tag vX.Y.Z')
    folder = lab.ROOT / 'upstream/3x-ui' / version
    archive = folder / 'x-ui-linux-amd64.tar.gz'
    checksum = archive.with_suffix('.gz.sha256')
    base = 'https://github.com/MHSanaei/3x-ui/releases/download/' + version + '/'
    download(base + archive.name, archive)
    download(base + checksum.name, checksum)
    with archive.open('rb') as file:
        actual = hashlib.file_digest(file, 'sha256').hexdigest()
    if actual != checksum.read_text().split()[0]:
        raise RuntimeError('Official release SHA256 mismatch')
    stage = lab.ROOT / 'artifacts/staging' / version
    stage.mkdir(parents=True, exist_ok=True)
    with tarfile.open(archive) as file:
        file.extractall(stage, filter='data')
    return stage / 'x-ui', actual

def activate(source):
    for name in ('x-ui', 'bin'):
        entry = source / name
        if entry.is_dir():
            shutil.copytree(entry, lab.PANEL / name, dirs_exist_ok=True)
        else:
            shutil.copy2(entry, lab.PANEL / name)
    (lab.PANEL / 'x-ui').chmod(0o755)
    (lab.PANEL / 'bin/xray-linux-amd64').chmod(0o755)

def main():
    parser = argparse.ArgumentParser()
    parser.add_argument('version')
    args = parser.parse_args()
    previous_version = 'v' + lab.state()['panel_version'].lstrip('v')
    if not re.fullmatch(r'v\d+\.\d+\.\d+', args.version):
        raise ValueError('Expected stable official tag vX.Y.Z')
    if tuple(map(int, args.version[1:].split('.'))) < tuple(map(int, previous_version[1:].split('.'))):
        raise RuntimeError('Database downgrades are not supported. Use a separate test environment.')
    target, sha = release(args.version)
    previous, _ = release(previous_version)
    lab.stop_one('panel')
    try:
        activate(target)
        lab.start_one('panel')
        lab.wait_panel()
    except Exception:
        lab.stop_one('panel')
        activate(previous)
        lab.start_one('panel')
        raise
    s = lab.state()
    s['panel_version'] = args.version.removeprefix('v')
    lab.write_private(lab.STATE, s)
    # Detect rather than overwrite a user's changed template during upgrades.
    raw = lab.api('xray/', {}, 'POST')
    wrapper = json.loads(raw) if isinstance(raw, str) else raw
    template = wrapper['xraySetting']
    if isinstance(template, str):
        template = json.loads(template)
    log_ok = template.get('log', {}).get('access') == str(lab.LOGS / 'xray-access.log')
    report = {'from': previous_version, 'to': args.version, 'sha256': sha, 'panel_ready': True,
              'access_log_config_preserved': log_ok, 'monitor_storage_untouched': True}
    (lab.ROOT / 'artifacts/reports/last-upgrade.json').write_text(json.dumps(report, indent=2) + '\n')
    print(json.dumps(report, ensure_ascii=False))
    if not log_ok:
        raise RuntimeError('Access log path changed; inspect configuration before using lab.py configure-log')

if __name__ == '__main__':
    main()
