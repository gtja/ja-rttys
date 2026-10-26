import importlib.util
from contextlib import closing, contextmanager
import io
from pathlib import Path
import platform
import sqlite3
import tarfile
import tempfile
import unittest
from unittest.mock import patch

ROOT = Path(__file__).resolve().parents[2]


def load(name):
    spec = importlib.util.spec_from_file_location(name, ROOT / 'scripts' / (name + '.py'))
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module


package = load('ci-package')
deploy = load('ci-deploy')
COMMIT = 'a' * 40
ELF_AMD64 = b'\x7fELF\x02\x01' + b'\0' * 12 + b'\x3e\x00' + b'verified-binary'


@contextmanager
def database_connection(path):
    with closing(sqlite3.connect(path)) as db:
        with db:
            yield db


class DeliveryTest(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.root = Path(self.temp.name)
        self.binary = self.root / 'rttys'
        self.binary.write_bytes(b'old-server')
        self.binary.chmod(0o755)
        self.new = self.root / 'new'
        self.new.write_bytes(b'new-server')
        self.database = self.root / 'identity.db'
        with database_connection(self.database) as db:
            db.executescript('PRAGMA user_version=2; CREATE TABLE proof(value TEXT); INSERT INTO proof VALUES ("original");')
        self.backup = self.root / 'backup'
        self.backup.mkdir()
        self.config = dict(binary=str(self.binary), database=str(self.database), service='test-only')
        self.metadata = dict(binary_sha256=deploy.sha256(self.new), source_commit=COMMIT)

    def run_upgrade(self, readiness=None):
        with patch.object(deploy, 'check_running_files'), patch.object(deploy, 'health'), \
             patch.object(deploy, 'systemctl', return_value='0') as ctl, \
             patch.object(deploy, 'wait_ready', side_effect=readiness):
            result = deploy.upgrade(self.config, self.new, self.metadata, self.backup)
        return result, ctl

    def test_success_backs_up_stopped_database_and_installs_selected_binary(self):
        result, _ = self.run_upgrade()
        self.assertEqual(result['status'], 'passed')
        self.assertEqual(self.binary.read_bytes(), b'new-server')
        self.assertEqual((self.backup / 'rttys.previous').read_bytes(), b'old-server')
        with database_connection(self.backup / 'identity.db') as db:
            self.assertEqual(db.execute('SELECT value FROM proof').fetchall(), [('original',)])

    def test_failed_acceptance_restores_binary_and_preserves_new_database_writes(self):
        calls = []

        def readiness(config, digest):
            calls.append(digest)
            if len(calls) == 1:
                with database_connection(self.database) as db:
                    db.execute('INSERT INTO proof VALUES ("new-write")')
                raise RuntimeError('deliberate acceptance failure')

        result, _ = self.run_upgrade(readiness)
        self.assertEqual((result['status'], result['rollback']), ('failed', 'passed'))
        self.assertEqual(self.binary.read_bytes(), b'old-server')
        self.assertEqual(calls, [deploy.sha256(self.new), deploy.sha256(self.binary)])
        with database_connection(self.database) as db:
            self.assertEqual(db.execute('SELECT value FROM proof').fetchall(), [('original',), ('new-write',)])

    def test_unsupported_old_schema_restarts_unchanged_service(self):
        with database_connection(self.database) as db:
            db.execute('PRAGMA user_version=1')
        result, ctl = self.run_upgrade()
        self.assertEqual((result['status'], result['rollback']), ('failed', 'passed'))
        self.assertEqual(self.binary.read_bytes(), b'old-server')
        self.assertIn(unittest.mock.call('start', 'test-only'), ctl.call_args_list)

    def test_unexpected_migration_blocks_unsafe_binary_downgrade(self):
        def readiness(config, digest):
            with database_connection(self.database) as db:
                db.execute('PRAGMA user_version=3')
            raise RuntimeError('new binary changed schema')

        result, ctl = self.run_upgrade(readiness)
        self.assertEqual((result['status'], result['rollback']), ('failed', 'failed'))
        self.assertIn('Schema changed', result['rollback_error'])
        self.assertEqual([c for c in ctl.call_args_list if c.args[0] == 'start'], [unittest.mock.call('start', 'test-only')])

    def make_archive(self):
        directory = self.root / 'package'
        directory.mkdir()
        (directory / 'rttys').write_bytes(ELF_AMD64)
        package.bundle(directory, 'local-test', COMMIT, 'amd64', 'test/image:local', 'sha256:example')
        archive = next(directory.glob('*.tar.gz'))
        return archive

    def test_bundle_roundtrip_verifies_commit_checksum_and_binary(self):
        archive = self.make_archive()
        stage = self.root / 'stage'
        stage.mkdir()
        with patch.object(platform, 'machine', return_value='x86_64'):
            binary, metadata = deploy.unpack(archive, deploy.sha256(archive), COMMIT, stage)
        self.assertEqual(binary.read_bytes(), ELF_AMD64)
        self.assertEqual(metadata['source_commit'], COMMIT)
        self.assertFalse(metadata['pushed'])

    def test_packaging_rejects_wrong_binary_architecture(self):
        directory = self.root / 'wrong-arch'
        directory.mkdir()
        (directory / 'rttys').write_bytes(ELF_AMD64)
        with self.assertRaisesRegex(ValueError, 'ELF architecture'):
            package.bundle(directory, 'local', COMMIT, 'arm64', 'test/image:local', 'sha256:example')

    def test_corrupt_package_and_wrong_commit_or_architecture_rejected(self):
        archive = self.make_archive()
        for digest, commit, machine in [('0' * 64, COMMIT, 'x86_64'), (deploy.sha256(archive), 'b' * 40, 'x86_64'), (deploy.sha256(archive), COMMIT, 'aarch64')]:
            with self.subTest(digest=digest, commit=commit, machine=machine), patch.object(platform, 'machine', return_value=machine):
                with self.assertRaises(ValueError):
                    deploy.unpack(archive, digest, commit, self.root)

    def test_tar_symlink_binary_is_not_extracted(self):
        archive = self.root / 'symlink.tar.gz'
        with tarfile.open(archive, 'w:gz') as tar:
            manifest = tarfile.TarInfo('manifest.json')
            manifest.size = 2
            tar.addfile(manifest, io.BytesIO(b'{}'))
            binary = tarfile.TarInfo('rttys')
            binary.type = tarfile.SYMTYPE
            binary.linkname = '/etc/passwd'
            tar.addfile(binary)
        with self.assertRaisesRegex(ValueError, 'Invalid package members'):
            deploy.unpack(archive, deploy.sha256(archive), COMMIT, self.root)


if __name__ == '__main__':
    unittest.main()
