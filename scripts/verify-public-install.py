#!/usr/bin/env python3
"""Verify the published GitHub installer and release in the isolated WSL daemon.

No download mocks or authentication are used. All fixture data stays in artifacts.
Docker, Compose and Python are preinstalled; this does not test fresh-OS apt setup.
"""
import argparse
from datetime import datetime, timezone
import hashlib
import json
import os
from pathlib import Path
import runpy
import shutil
import signal
import socket
import subprocess
import sys
import tarfile
import urllib.error
import urllib.request
import uuid

ROOT = Path(__file__).resolve().parents[1]
VERSION = 'v1.0.2'
REPO = 'eatfatno/x-ui-tab'
SCRIPT_URL = f'https://api.github.com/repos/{REPO}/contents/scripts/install-online.sh?ref={VERSION}'
RAW_URL = f'https://raw.githubusercontent.com/{REPO}/{VERSION}/scripts/install-online.sh'
RELEASE_URL = f'https://api.github.com/repos/{REPO}/releases/tags/{VERSION}'
BASE = ROOT / 'artifacts/fixtures' / ('public-install-' + uuid.uuid4().hex[:8])
STACK = BASE / 'deployment'
REPORT = ROOT / 'artifacts/reports/public-online-install.json'
PORTS = (28084, 22056, 63792, 21114)
PREFIX = '/github-check/'


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument('--offline-bundle', type=Path,
                        help='Verify a separately downloaded public archive and install it offline')
    options = parser.parse_args()
    report_path = REPORT if options.offline_bundle is None else REPORT.with_name('public-bundle-install.json')
    if os.geteuid() != 0:
        raise RuntimeError('Run as root in WSL')
    expected = 'unix://' + str(ROOT / 'cache/docker-engine/fs/docker.sock')
    if os.environ.get('DOCKER_HOST') != expected or os.environ.get('DOCKER_CONTEXT'):
        raise RuntimeError('Use only the isolated project Docker daemon')
    if os.environ.get('PYTHONPATH') or os.environ.get('INSTALL_TEST_ROOT'):
        raise RuntimeError('Remove download fixture overrides before the public test')
    for port in PORTS:
        with socket.socket() as sock:
            sock.bind(('127.0.0.1', port))
    os.umask(0o077)
    BASE.mkdir(parents=True)
    report = {
        'passed': False, 'started_at': datetime.now(timezone.utc).isoformat(),
        'repository': REPO, 'version': VERSION, 'fixture': str(BASE.relative_to(ROOT)),
        'script_url': SCRIPT_URL, 'download_source': 'public GitHub HTTPS, no mocks or credentials',
        'fresh_os_dependency_install_tested': False, 'checks': [],
        'installation_mode': 'public curl pipe' if options.offline_bundle is None else 'separate public download and offline installation',
    }

    def passed(label):
        report['checks'].append(label)
        report_path.write_text(json.dumps(report, indent=2) + '\n')
        print('PASS', label, flush=True)

    def fetch(url):
        request = urllib.request.Request(url, headers={
            'Accept': 'application/vnd.github.raw+json' if url == SCRIPT_URL else 'application/vnd.github+json',
            'User-Agent': 'x-ui-tab-public-verification',
        })
        with urllib.request.urlopen(request, timeout=30) as response:
            if not response.url.startswith('https://'):
                raise ValueError('Unexpected non-HTTPS response')
            data = response.read(2 * 1024**2 + 1)
            if len(data) > 2 * 1024**2:
                raise ValueError('Unexpected response size')
            return data

    def cli(*args, check=True):
        result = subprocess.run([sys.executable, str(STACK / 'x-ui'), *args],
                                capture_output=True, text=True, timeout=240)
        with (BASE / 'management.log').open('a') as log:
            log.write(result.stdout + result.stderr)
        if check and result.returncode:
            raise RuntimeError('Management command failed: ' + args[0])
        return result

    try:
        try:
            raw = fetch(RAW_URL)
            report['raw_url_available'] = raw == (ROOT / 'scripts/install-online.sh').read_bytes()
        except Exception as error:
            report['raw_url_available'] = False
            report['raw_url_error_type'] = type(error).__name__

        release = json.loads(fetch(RELEASE_URL))
        assert release['tag_name'] == VERSION and not release['draft'] and not release['prerelease']
        bundle_name = 'x-ui-tab-3.10.2-1.0.2-linux-amd64.tar.gz'
        assets = {a['name']: a for a in release['assets']}
        assert bundle_name in assets and bundle_name + '.sha256' in assets
        report['release_url'] = release['html_url']
        report['release_asset_url'] = assets[bundle_name]['browser_download_url']
        report['release_asset_bytes'] = assets[bundle_name]['size']
        passed('stable release and both public assets are accessible without authentication')

        script = fetch(SCRIPT_URL)
        assert script.startswith(b'#!/usr/bin/env bash\n')
        assert script == (ROOT / 'scripts/install-online.sh').read_bytes(), 'Published installer differs from reviewed source'
        report['installer_sha256'] = hashlib.sha256(script).hexdigest()
        (BASE / 'install-online.sh').write_bytes(script)
        passed('GitHub API returns the exact published shell installer')

        args = ['bash', '-s', '--', '--version', VERSION, '--no-install-deps',
                '--dir', str(STACK), '--bin-dir', str(BASE / 'bin'), '--bind', '127.0.0.1',
                '--port', str(PORTS[0]), '--panel-port', str(PORTS[1]),
                '--api-port', str(PORTS[2]), '--metrics-port', str(PORTS[3]),
                '--base-path', PREFIX, '--project-name', 'x-ui-public-check']
        if options.offline_bundle is None:
            # Exercise the published curl | bash form, including its actual remote fetch.
            with (BASE / 'installation.log').open('wb') as log:
                download = subprocess.Popen(['curl', '--fail', '--silent', '--show-error', '--location',
                                             '--connect-timeout', '15', '--max-time', '60',
                                             '-H', 'Accept: application/vnd.github.raw+json', SCRIPT_URL],
                                            stdout=subprocess.PIPE, stderr=log, start_new_session=True)
                install = subprocess.Popen(args, stdin=download.stdout, stdout=log, stderr=log,
                                           start_new_session=True)
                download.stdout.close()
                try:
                    install_code = install.wait(timeout=1500)
                    download_code = download.wait(timeout=30)
                except BaseException:
                    for proc in (install, download):
                        if proc.poll() is None:
                            os.killpg(proc.pid, signal.SIGTERM)
                    for proc in (install, download):
                        try:
                            proc.wait(timeout=10)
                        except subprocess.TimeoutExpired:
                            os.killpg(proc.pid, signal.SIGKILL)
                            proc.wait()
                    raise
            assert download_code == 0 and install_code == 0, 'Public installer failed; inspect the private fixture log'
            installed = json.loads((STACK / '.online-install.json').read_text())
            assert 'sha256:' + installed['sha256'] == assets[bundle_name]['digest']
            report['bundle_sha256'] = installed['sha256']
            passed('real curl pipe downloads, verifies, extracts, loads and starts the public release')
        else:
            archive = options.offline_bundle.resolve()
            assert archive.is_relative_to(ROOT) and archive.is_file()
            with archive.open('rb') as f:
                digest = hashlib.file_digest(f, 'sha256').hexdigest()
            assert 'sha256:' + digest == assets[bundle_name]['digest']
            assert archive.stat().st_size == assets[bundle_name]['size']
            report['bundle_sha256'] = digest
            report['public_download_archive'] = str(archive.relative_to(ROOT))
            unpacked = BASE / 'unpacked'
            with tarfile.open(archive, 'r:gz') as tar:
                tar.extractall(unpacked, filter='data')
            shutil.copytree(unpacked / bundle_name.removesuffix('.tar.gz'), STACK)
            with (BASE / 'installation.log').open('w') as log:
                subprocess.run(['sha256sum', '-c', 'SHA256SUMS'], cwd=STACK,
                               stdout=log, stderr=log, check=True, timeout=120)
                subprocess.run(['bash', str(STACK / 'install.sh'), str(BASE / 'bin')],
                               stdout=log, stderr=log, check=True, timeout=30)
            cli('load')
            cli('init', '--bind', '127.0.0.1', '--port', str(PORTS[0]), '--panel-port', str(PORTS[1]),
                '--api-port', str(PORTS[2]), '--metrics-port', str(PORTS[3]),
                '--base-path', PREFIX, '--project-name', 'x-ui-public-check')
            passed('actual public archive matches GitHub digest and all file hashes, then installs offline')

        manifest = json.loads((STACK / 'bundle.json').read_text())
        ident = subprocess.check_output(['docker', 'image', 'inspect', '-f', '{{.Id}}', manifest['image']], text=True).strip()
        assert ident == manifest['image_id']
        assert (BASE / 'bin/x-ui').resolve() == STACK / 'x-ui'
        report['image_id'] = ident
        passed('loaded image identity and installed Linux management entry match the release')

        Browser = runpy.run_path(str(ROOT / 'scripts/verify-runtime.py'))['Browser']
        origin = f'http://127.0.0.1:{PORTS[0]}'
        browser, anonymous = Browser(origin), Browser(origin)
        assert anonymous.request(PREFIX + 'traffic-monitor/api/v1/clients')[0] == 401
        credentials = json.loads((STACK / 'data/state/credentials.json').read_text())
        browser.obj(PREFIX + 'login', credentials, {'X-CSRF-Token': browser.obj(PREFIX + 'csrf-token')})
        assert browser.obj(PREFIX + 'traffic-monitor/api/v1/clients') == []
        assert browser.obj(PREFIX + 'traffic-monitor/api/v1/summary')['total'] == 0
        health = browser.obj(PREFIX + 'traffic-monitor/api/v1/health')
        assert health['status'] == 'ok' and health['detail_days'] == 7 and health['summary_days'] == 35
        passed('fresh login, authentication boundary, empty data and 7/35 day retention work')

        panel = browser.request(PREFIX + 'panel/?monitor=traffic')
        monitor = browser.request(PREFIX + 'traffic-monitor/')
        assert panel[0] == monitor[0] == 200
        assert b'traffic-monitor' in panel[2] and b'<html' in monitor[2]
        cli('status')
        cli('settings')
        state = credentials | {'public_url': origin, 'base_path': PREFIX}
        (BASE / 'browser-state.json').write_text(json.dumps(state))
        env = os.environ | {'PANEL_STATE_FILE': str(BASE / 'browser-state.json'),
                            'PUBLIC_INSTALL_FIXTURE': str(BASE)}
        with (BASE / 'browser.log').open('w') as log:
            checked = subprocess.run(['node', str(ROOT / 'scripts/browser-tests/public-install.mjs')],
                                     env=env, stdout=log, stderr=log, timeout=180)
        assert checked.returncode == 0, 'Browser check failed; inspect fixture browser log'
        report['browser'] = json.loads((BASE / 'browser-report.json').read_text())
        passed('published package opens the embedded and independent pages in Chromium')

        before = (STACK / 'data/state/credentials.json').read_bytes()
        repeat = subprocess.run(args, input=script, capture_output=True, timeout=30)
        assert repeat.returncode != 0 and b'existing or partially initialized' in repeat.stderr
        assert (STACK / 'data/state/credentials.json').read_bytes() == before
        passed('reinstall refuses to overwrite existing data and credentials')
        report['passed'] = True
    except Exception as error:
        report['error_type'] = type(error).__name__
        raise
    finally:
        if (STACK / '.env').exists():
            try:
                report['test_container_removed'] = cli('remove', '--yes', check=False).returncode == 0
            except Exception:
                report['test_container_removed'] = False
            if not report['test_container_removed']:
                report['passed'] = False
        report['completed_at'] = datetime.now(timezone.utc).isoformat()
        report_path.write_text(json.dumps(report, indent=2) + '\n')
    print(json.dumps(report, indent=2))


if __name__ == '__main__':
    main()
