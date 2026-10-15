#!/usr/bin/env python3
"""Exercise the installer's download/extraction boundary without a published release."""
from contextlib import redirect_stdout, redirect_stderr
import hashlib
import io
import json
import os
from pathlib import Path
import re
import subprocess
import sys
import tarfile
import tempfile
import unittest
from unittest.mock import patch

ROOT = Path(__file__).resolve().parents[1]
SCRIPT = ROOT / 'scripts/install-online.sh'
CODE = SCRIPT.read_text().split("<<'PY'\n", 1)[1].split('\nPY\n', 1)[0]
REPO, VERSION = 'eatfatno/x-ui-tab', 'v1.0.2'
NAME = 'x-ui-tab-3.10.2-1.0.2-linux-amd64'


def digest(data):
    return hashlib.sha256(data).hexdigest()


class Response(io.BytesIO):
    def __init__(self, data, url, status=200, headers=None):
        super().__init__(data)
        self.url = url
        self.status = status
        self.headers = headers or {}


class InstallerTests(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory(prefix='installer-test-', dir=ROOT / 'cache/tmp')
        self.target = Path(self.temp.name) / 'deployment'
        (self.target / '.cache/online-install').mkdir(parents=True)
        self.files = {
            'x-ui': b'#!/usr/bin/env python3\n',
            'install.sh': b'#!/usr/bin/env bash\n',
            'compose.yaml': b'services: {}\n',
            'image.tar.gz': b'fixture-image',
        }
        self.manifest = {'architecture': 'amd64', 'monitor_version': '1.0.2',
                         'image_archive_sha256': digest(self.files['image.tar.gz'])}
        self.release = {'tag_name': VERSION, 'draft': False, 'prerelease': False,
                        'assets': [{'name': NAME + '.tar.gz'}, {'name': NAME + '.tar.gz.sha256'}]}
        self.requests = []
        self.archive_extra = None
        self.wrong_archive_hash = False
        self.inventory_missing = False
        self.wrong_range = False
        self.truncate_once = False
        self.range_calls = 0
        self.wrong_host = False

    def tearDown(self):
        self.temp.cleanup()

    def assets(self):
        files = self.files | {'bundle.json': json.dumps(self.manifest).encode()}
        inventory = ''.join(digest(data) + '  ' + name + '\n' for name, data in files.items()
                            if not (self.inventory_missing and name == 'compose.yaml'))
        stream = io.BytesIO()
        with tarfile.open(fileobj=stream, mode='w:gz') as tar:
            for name, data in (files | {'SHA256SUMS': inventory.encode()}).items():
                info = tarfile.TarInfo(NAME + '/' + name)
                info.size = len(data)
                tar.addfile(info, io.BytesIO(data))
            if self.archive_extra:
                tar.addfile(self.archive_extra)
        archive = stream.getvalue()
        checksum = ('0' * 64 if self.wrong_archive_hash else digest(archive)) + '  ' + NAME + '.tar.gz\n'
        return archive, checksum.encode()

    def install(self):
        archive, checksum = self.assets()
        prefix = f'https://api.github.com/repos/{REPO}/releases/assets/'
        self.release['assets'] = [{'name': NAME + '.tar.gz', 'url': prefix + '1', 'size': len(archive)},
                                  {'name': NAME + '.tar.gz.sha256', 'url': prefix + '2', 'size': len(checksum)}]
        signed = 'https://release-assets.githubusercontent.com/fixture'

        def response(request, timeout):
            url = request.full_url
            self.requests.append(url)
            if url == prefix + '1' and request.get_method() == 'HEAD':
                return Response(b'', 'https://example.invalid/fixture' if self.wrong_host else signed)
            if url == signed:
                start, end = map(int, request.get_header('Range').removeprefix('bytes=').split('-'))
                self.range_calls += 1
                data = archive[start:end + 1]
                if self.truncate_once and start == 0:
                    self.truncate_once = False
                    data = data[:-1]
                return Response(data, signed, 206, {'Content-Range': 'invalid' if self.wrong_range else f'bytes {start}-{end}/{len(archive)}'})
            urls = {
                f'https://api.github.com/repos/{REPO}/releases/tags/{VERSION}': json.dumps(self.release).encode(),
                prefix + '1': archive,
                prefix + '2': checksum,
            }
            if url not in urls:
                raise AssertionError('Unexpected network request')
            return Response(urls[url], url)

        with patch('sys.argv', ['installer', REPO, VERSION, str(self.target)]), \
                patch('urllib.request.urlopen', side_effect=response), \
                redirect_stdout(io.StringIO()), redirect_stderr(io.StringIO()):
            exec(compile(CODE, str(SCRIPT), 'exec'), {'__name__': '__main__'})

    def test_verified_install_and_fresh_retry(self):
        self.install()
        self.assertEqual((self.target / 'image.tar.gz').read_bytes(), self.files['image.tar.gz'])
        self.assertEqual(json.loads((self.target / '.online-install.json').read_text())['repository'], REPO)
        self.assertFalse(list((self.target / '.cache/online-install').iterdir()))
        self.install()
        self.assertTrue(os.access(self.target / 'x-ui', os.X_OK))

    def large_image(self):
        self.files['image.tar.gz'] = os.urandom(2 * 1024**2 + 500)
        self.manifest['image_archive_sha256'] = digest(self.files['image.tar.gz'])

    def test_range_download_retries_incomplete_part_and_verifies_full_archive(self):
        self.large_image()
        self.truncate_once = True
        self.install()
        self.assertGreaterEqual(self.range_calls, 4)
        self.assertEqual((self.target / 'image.tar.gz').read_bytes(), self.files['image.tar.gz'])

    def test_rejects_wrong_range(self):
        self.large_image()
        self.wrong_range = True
        with self.assertRaisesRegex(ValueError, 'byte range'):
            self.install()
        self.assertFalse((self.target / 'x-ui').exists())

    def test_rejects_unexpected_download_host(self):
        self.large_image()
        self.wrong_host = True
        with self.assertRaisesRegex(ValueError, 'download host'):
            self.install()

    def test_rejects_checksum_mismatch_before_install(self):
        self.wrong_archive_hash = True
        with self.assertRaisesRegex(ValueError, 'checksum mismatch'):
            self.install()
        self.assertFalse((self.target / 'x-ui').exists())

    def test_rejects_traversal(self):
        self.archive_extra = tarfile.TarInfo(NAME + '/../../escape')
        with self.assertRaisesRegex(ValueError, 'unsafe'):
            self.install()
        self.assertFalse((self.target.parent / 'escape').exists())

    def test_rejects_symlink(self):
        self.archive_extra = tarfile.TarInfo(NAME + '/docs/link')
        self.archive_extra.type = tarfile.SYMTYPE
        self.archive_extra.linkname = '/etc/passwd'
        with self.assertRaisesRegex(ValueError, 'unsafe'):
            self.install()

    def test_rejects_runtime_data_in_release(self):
        self.archive_extra = tarfile.TarInfo(NAME + '/data/credentials.json')
        with self.assertRaisesRegex(ValueError, 'unexpected'):
            self.install()

    def test_rejects_incomplete_inventory(self):
        self.inventory_missing = True
        with self.assertRaisesRegex(ValueError, 'inventory does not match'):
            self.install()

    def test_rejects_wrong_architecture(self):
        self.manifest['architecture'] = 'arm64'
        with self.assertRaisesRegex(ValueError, 'architecture or version'):
            self.install()

    def test_rejects_wrong_version(self):
        self.manifest['monitor_version'] = '9.9.9'
        with self.assertRaisesRegex(ValueError, 'architecture or version'):
            self.install()

    def test_rejects_changed_image_digest(self):
        self.manifest['image_archive_sha256'] = '0' * 64
        with self.assertRaisesRegex(ValueError, 'Image archive'):
            self.install()

    def test_rejects_prerelease(self):
        self.release['prerelease'] = True
        with self.assertRaisesRegex(ValueError, 'stable release'):
            self.install()
        self.assertEqual(len(self.requests), 1)

    def test_preserves_unrelated_directory(self):
        (self.target / 'personal-file').write_text('keep')
        with self.assertRaisesRegex(ValueError, 'unrelated files'):
            self.install()
        self.assertEqual((self.target / 'personal-file').read_text(), 'keep')
        self.assertFalse((self.target / 'x-ui').exists())

    def test_preserves_existing_file_with_same_program_name(self):
        (self.target / 'x-ui').write_text('existing unrelated program')
        with self.assertRaisesRegex(ValueError, 'unrelated files'):
            self.install()
        self.assertEqual((self.target / 'x-ui').read_text(), 'existing unrelated program')

    def test_refuses_retry_for_another_version(self):
        self.install()
        path = self.target / '.online-install.json'
        marker = json.loads(path.read_text())
        marker['version'] = 'v0.9.0'
        path.write_text(json.dumps(marker))
        with self.assertRaisesRegex(ValueError, 'another repository or version'):
            self.install()

    def test_shell_argument_validation(self):
        for args in (['--repo', '../private'], ['--version', 'latest'], ['--port', '65536'], ['--dir', 'relative'], ['--port']):
            result = subprocess.run(['bash', str(SCRIPT), *args], capture_output=True)
            self.assertNotEqual(result.returncode, 0)
        self.assertEqual(subprocess.run(['bash', str(SCRIPT), '--help'], capture_output=True).returncode, 0)


if __name__ == '__main__':
    unittest.main(verbosity=2)
