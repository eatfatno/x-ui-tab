#!/usr/bin/env python3
"""Regression: health reads must not create the first-start database."""
import ast
from contextlib import closing
from pathlib import Path
import sqlite3
import tempfile
import unittest

ROOT = Path(__file__).resolve().parents[1]


class InitializationTests(unittest.TestCase):
    def test_health_reads_leave_fresh_install_fresh(self):
        # Load the actual reader without requiring container /app and /data.
        tree = ast.parse((ROOT / 'deploy/docker/container.py').read_text())
        reader = next(node for node in tree.body if isinstance(node, ast.FunctionDef) and node.name == 'settings')
        with tempfile.TemporaryDirectory(dir=ROOT / 'cache/tmp', prefix='init-race-') as folder:
            dbpath = Path(folder) / 'x-ui.db'
            env = {'DB': dbpath, 'sqlite3': sqlite3, 'closing': closing}
            exec(compile(ast.Module(body=[reader], type_ignores=[]), 'container.py', 'exec'), env)
            for _ in range(3):
                with self.assertRaises(sqlite3.OperationalError):
                    env['settings']()
                self.assertFalse(dbpath.exists(), 'A health check created the uninitialized panel database')
            with closing(sqlite3.connect(dbpath)) as db, db:
                db.execute('CREATE TABLE settings (key TEXT, value TEXT)')
                db.execute("INSERT INTO settings VALUES ('webBasePath','/fresh/')")
            self.assertEqual(env['settings'](), {'webBasePath': '/fresh/'})


if __name__ == '__main__':
    unittest.main(verbosity=2)
