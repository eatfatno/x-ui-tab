#!/usr/bin/env python3
"""Migration-specific checks: new inodes, unread tails, tampered snapshots."""
import hashlib
from contextlib import closing
import json
from pathlib import Path
import runpy
import shutil
import sqlite3
import tempfile
import unittest

ROOT = Path(__file__).resolve().parents[1]


class RestoreCheckpoints(unittest.TestCase):
    def setUp(self):
        self.tmp = tempfile.TemporaryDirectory(prefix='docker-checkpoint-', dir=ROOT / 'cache/tmp')
        self.root = Path(self.tmp.name)
        shutil.copy2(ROOT / 'deploy/docker/x-ui', self.root / 'x-ui')
        self.manager = runpy.run_path(str(self.root / 'x-ui'))
        data = self.root / 'data'
        for name in ('monitor', 'state', 'logs'):
            (data / name).mkdir(parents=True)
        self.file = data / 'logs/xray-access.log'
        self.file.write_bytes(b'already committed\nunread complete\npartial')
        self.offset = len(b'already committed\n')
        self.old_id = self.identity(self.file)
        with closing(sqlite3.connect(data / 'monitor/monitor.db')) as db, db:
            db.execute('CREATE TABLE checkpoints(file_id TEXT PRIMARY KEY,offset INTEGER,anchor TEXT,first_seen INTEGER,updated_at INTEGER)')
            db.execute('INSERT INTO checkpoints VALUES(?,?,?,?,?)',
                       (self.old_id, self.offset, hashlib.sha256(b'already committed\n').hexdigest(), 1, 2))
        self.manager['checkpoint_manifest']()
        self.stage = self.root / 'restored'
        shutil.copytree(data, self.stage / 'data')

    def tearDown(self):
        self.tmp.cleanup()

    def identity(self, file):
        st = file.stat()
        return str(st.st_dev) + ':' + str(st.st_ino)

    def test_restore_rebinds_inode_and_preserves_unread_tail(self):
        file = self.stage / 'data/logs/xray-access.log'
        self.assertNotEqual(self.identity(file), self.old_id)
        self.manager['restore_checkpoints'](self.stage)
        with closing(sqlite3.connect(self.stage / 'data/monitor/monitor.db')) as db:
            rows = db.execute('SELECT file_id,offset FROM checkpoints').fetchall()
        self.assertEqual(rows, [(self.identity(file), self.offset)])
        self.assertEqual(file.read_bytes()[rows[0][1]:], b'unread complete\npartial')

    def test_changed_log_rejected_without_changing_checkpoint(self):
        file = self.stage / 'data/logs/xray-access.log'
        file.write_bytes(b'content replaced')
        with self.assertRaisesRegex(ValueError, 'does not match'):
            self.manager['restore_checkpoints'](self.stage)
        with closing(sqlite3.connect(self.stage / 'data/monitor/monitor.db')) as db:
            self.assertEqual(db.execute('SELECT file_id FROM checkpoints').fetchone()[0], self.old_id)

    def test_missing_manifest_and_unaccounted_log_are_rejected(self):
        extra = self.stage / 'data/logs/xray-access.log.1'
        extra.write_bytes(b'unknown log\n')
        with self.assertRaisesRegex(ValueError, 'unaccounted'):
            self.manager['restore_checkpoints'](self.stage)
        (self.stage / 'data/state/backup-checkpoints.json').unlink()
        with self.assertRaisesRegex(ValueError, 'no log checkpoint manifest'):
            self.manager['restore_checkpoints'](self.stage)


if __name__ == '__main__':
    unittest.main(verbosity=2)
