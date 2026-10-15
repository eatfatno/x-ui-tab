#!/usr/bin/env python3
"""Create a portable offline bundle and its source package, using allowlists."""
import gzip
import hashlib
import json
from pathlib import Path
import shutil
import subprocess
import tarfile

ROOT = Path(__file__).resolve().parents[1]


def sha(path):
    with path.open('rb') as f:
        return hashlib.file_digest(f, 'sha256').hexdigest()


def main():
    manifest = json.loads((ROOT / 'artifacts/docker/context/manifest.json').read_text())
    name = 'x-ui-tab-' + manifest['panel_version'] + '-' + manifest['monitor_version'] + '-linux-amd64'
    bundle = ROOT / 'artifacts/docker' / name
    bundle.mkdir(parents=True, exist_ok=True)
    # Repackaging must never pick up a deployment's data or credentials.
    if (bundle / 'data').exists() or (bundle / '.env').exists():
        raise RuntimeError('Bundle directory contains runtime data; choose a clean output version')
    for entry in ('compose.yaml', 'x-ui', 'install.sh'):
        shutil.copy2(ROOT / 'deploy/docker' / entry, bundle / entry)
    for entry in ('x-ui', 'install.sh'):
        (bundle / entry).chmod(0o755)
    docs = bundle / 'docs'
    docs.mkdir(exist_ok=True)
    for entry in ('docker-deployment.md', 'local-development.md', 'resource-assessment.md', 'targets-and-deletion.md', 'test-matrix.md', 'github-publishing.md', 'upgrade-v1.0.2.md'):
        if (ROOT / 'docs' / entry).exists():
            shutil.copy2(ROOT / 'docs' / entry, docs / entry)
    shutil.copytree(ROOT / 'artifacts/docker/context/licenses', bundle / 'licenses', dirs_exist_ok=True)
    shutil.copy2(ROOT / 'LICENSE', bundle / 'licenses/traffic-monitor-LICENSE')
    shutil.copy2(ROOT / 'THIRD_PARTY_NOTICES.md', bundle / 'licenses/THIRD_PARTY_NOTICES.md')
    reports = bundle / 'reports'
    reports.mkdir(exist_ok=True)
    for report_name in ('docker-acceptance.json', 'docker-official-browser.json', 'docker-monitor-browser.json', 'panel-recovery-browser.json', 'docker-checkpoint-tests.txt', 'docker-backend-vet.txt', 'online-installer-tests.txt', 'docker-initialization-tests.txt', 'docker-version-upgrade.json', 'upgrade102-go-tests.txt', 'upgrade102-frontend-tests.txt', 'resource-reality-5clients.json', 'runtime-checks.json'):
        source_report = ROOT / 'artifacts/reports' / report_name
        if source_report.exists():
            shutil.copy2(source_report, reports / report_name)
    source = bundle / 'sources'
    source.mkdir(exist_ok=True)
    (source / 'UPSTREAM-SOURCE.txt').write_text('Unmodified official Linux release; corresponding upstream source:\n' + manifest['panel_source_url'] + '\n\nThe upstream source tarball was quarantined by Windows Defender during development and is not redistributed here. No security exclusion was added.\n')
    def allow(member):
        if any(p in ('node_modules', 'dist', '__pycache__', '.cache', '.env') for p in Path(member.name).parts):
            return None
        return member if member.isdir() or member.isfile() else None
    with tarfile.open(source / 'monitor-and-deployment-source.tar.gz', 'w:gz') as tar:
        for entry in ('apps/traffic-monitor', 'deploy/docker', 'docs', 'scripts', 'README.md', 'LICENSE', 'THIRD_PARTY_NOTICES.md', '.gitignore', '.gitattributes'):
            tar.add(ROOT / entry, arcname=entry, filter=allow)
    image = bundle / 'image.tar.gz'
    inspected = json.loads(subprocess.check_output(['docker', 'image', 'inspect', manifest['image']], text=True))[0]
    old = json.loads((bundle / 'bundle.json').read_text()) if (bundle / 'bundle.json').exists() else {}
    reusable = image.is_file() and old.get('image_id') == inspected['Id'] and old.get('image_archive_sha256') == sha(image)
    if not reusable:
        with image.with_suffix('.part').open('wb') as raw, gzip.GzipFile(fileobj=raw, mode='wb', compresslevel=2, mtime=0) as gz:
            proc = subprocess.Popen(['docker', 'save', manifest['image']], stdout=subprocess.PIPE)
            shutil.copyfileobj(proc.stdout, gz, 1024 * 1024)
            if proc.wait() != 0:
                raise RuntimeError('docker save failed')
        image.with_suffix('.part').replace(image)
    manifest.update(image_id=inspected['Id'], image_size_bytes=inspected['Size'], image_archive_sha256=sha(image))
    (bundle / 'bundle.json').write_text(json.dumps(manifest, indent=2) + '\n')
    files = sorted(p for p in bundle.rglob('*') if p.is_file() and p.name != 'SHA256SUMS')
    (bundle / 'SHA256SUMS').write_text(''.join(sha(p) + '  ' + p.relative_to(bundle).as_posix() + '\n' for p in files))
    archive = bundle.parent / (bundle.name + '.tar.gz')
    with tarfile.open(archive, 'w:gz', compresslevel=1) as tar:
        tar.add(bundle, arcname=bundle.name)
    archive.with_suffix('.gz.sha256').write_text(sha(archive) + '  ' + archive.name + '\n')
    print('Portable bundle:', archive)
    print('Image bytes:', inspected['Size'], 'bundle bytes:', archive.stat().st_size)


if __name__ == '__main__':
    main()
