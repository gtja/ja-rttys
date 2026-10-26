#!/usr/bin/env python3
"""Upgrade one existing Linux systemd instance. No provisioning or automatic DB downgrade."""
import argparse
from contextlib import closing
import fcntl
import hashlib
import json
import os
from pathlib import Path
import platform
import re
import shutil
import sqlite3
import ssl
import stat
import subprocess
import tarfile
import tempfile
import time
from urllib import error, parse, request


def sha256(path):
    digest = hashlib.sha256()
    with Path(path).open('rb') as stream:
        for chunk in iter(lambda: stream.read(1024 * 1024), b''):
            digest.update(chunk)
    return digest.hexdigest()


def schema(path):
    with closing(sqlite3.connect(path.as_uri() + '?mode=ro', uri=True, timeout=5)) as db:
        return db.execute('PRAGMA user_version').fetchone()[0]


def load_config(path):
    path = Path(path)
    info = path.lstat()
    if not stat.S_ISREG(info.st_mode) or info.st_uid != 0 or info.st_mode & 0o077:
        raise ValueError('Deploy config must be a root-owned regular file, mode 0600')
    config = json.loads(path.read_text())
    if not re.fullmatch(r'[A-Za-z0-9_.@-]+', config['service']):
        raise ValueError('Invalid systemd service name')
    for key in ['binary', 'database', 'backup_root', 'tls_ca']:
        value = Path(config[key])
        if not value.is_absolute() or value.is_symlink():
            raise ValueError('Deploy paths must be absolute and must not be symlinks')
    origin = parse.urlsplit(config['console_url'])
    if origin.scheme != 'https' or not origin.netloc or origin.username or origin.path not in ('', '/') or origin.query or origin.fragment:
        raise ValueError('console_url must be an HTTPS origin')
    return config


def unpack(archive, expected_sha, commit, destination):
    if not re.fullmatch(r'[0-9a-f]{40}', commit) or not re.fullmatch(r'[0-9a-f]{64}', expected_sha):
        raise ValueError('Invalid source commit or package SHA256')
    if sha256(archive) != expected_sha:
        raise ValueError('Package SHA256 mismatch')
    with tarfile.open(archive, 'r:gz') as tar:
        names = tar.getnames()
        if names.count('manifest.json') != 1 or names.count('rttys') != 1:
            raise ValueError('Package must contain one binary and one manifest')
        manifest = tar.getmember('manifest.json')
        binary = tar.getmember('rttys')
        if not manifest.isfile() or manifest.size > 16384 or not binary.isfile() or not 0 < binary.size <= 200 * 1024 * 1024:
            raise ValueError('Invalid package members')
        metadata = json.load(tar.extractfile(manifest))
        arch = {'x86_64': 'amd64', 'aarch64': 'arm64'}.get(platform.machine())
        identity_schema = metadata.get('identity_schema')
        if metadata.get('service') != 'ja-rttys' or metadata.get('source_commit') != commit or metadata.get('architecture') != arch or type(identity_schema) is not int or identity_schema <= 0:
            raise ValueError('Package service/commit/architecture/schema mismatch')
        target = Path(destination) / 'rttys'
        with tar.extractfile(binary) as source, target.open('xb') as output:
            shutil.copyfileobj(source, output)
        if sha256(target) != metadata['binary_sha256']:
            raise ValueError('Binary SHA256 mismatch')
        with target.open('rb') as source:
            header = source.read(20)
        if header[:6] != b'\x7fELF\x02\x01' or int.from_bytes(header[18:20], 'little') != {'amd64': 62, 'arm64': 183}.get(arch):
            raise ValueError('Binary ELF architecture does not match the host')
        return target, metadata


class NoRedirect(request.HTTPRedirectHandler):
    def redirect_request(self, *args, **kwargs):
        return None


def health(config):
    context = ssl.create_default_context(cafile=config['tls_ca'])
    client = request.build_opener(NoRedirect(), request.HTTPSHandler(context=context))
    base = config['console_url'].rstrip('/')
    with client.open(base + '/login', timeout=5) as response:
        if response.status != 200 or b'<html' not in response.read(1024 * 1024).lower():
            raise RuntimeError('Console HTML check failed')
    try:
        with client.open(base + '/api/me', timeout=5):
            raise RuntimeError('Unauthenticated API unexpectedly succeeded')
    except error.HTTPError as exc:
        if exc.code != 401:
            raise RuntimeError('Unauthenticated API must return 401') from None


def systemctl(*args):
    return subprocess.check_output(['systemctl', *args], text=True, stderr=subprocess.STDOUT).strip()


def wait_ready(config, digest):
    for attempt in range(15):
        try:
            pid = int(systemctl('show', config['service'], '--property=MainPID', '--value'))
            if pid <= 0 or sha256('/proc/{}/exe'.format(pid)) != digest:
                raise RuntimeError('Running binary differs from selected package')
            health(config)
            return
        except (OSError, ValueError, RuntimeError, error.URLError, subprocess.CalledProcessError):
            time.sleep(2)
    raise RuntimeError('New process or HTTPS/auth-boundary verification failed')


def check_running_files(config, old_digest):
    database = Path(config['database'])
    systemctl('is-active', '--quiet', config['service'])
    pid = int(systemctl('show', config['service'], '--property=MainPID', '--value'))
    if pid <= 0 or sha256('/proc/{}/exe'.format(pid)) != old_digest:
        raise RuntimeError('Configured binary differs from active service')
    # Bind the configured database to the running process before stopping it.
    if str(database.resolve()) not in [os.path.realpath(p) for p in Path('/proc/{}/fd'.format(pid)).glob('*')]:
        raise RuntimeError('Configured database is not open in the active service')


def upgrade(config, binary, metadata, backup):
    target, database = Path(config['binary']), Path(config['database'])
    expected_schema = metadata['identity_schema']
    old_info = target.stat()
    old_digest = sha256(target)
    check_running_files(config, old_digest)
    health(config)
    shutil.copy2(target, backup / 'rttys.previous')
    stopped = False
    installed = False
    try:
        # Set before requesting stop: a partial stop failure still needs recovery.
        stopped = True
        systemctl('stop', config['service'])
        if int(systemctl('show', config['service'], '--property=MainPID', '--value')) != 0:
            raise RuntimeError('Service did not stop')
        actual_schema = schema(database)
        if actual_schema != expected_schema:
            raise RuntimeError('Identity schema mismatch: running database v{}, package v{}; only same-schema upgrades are supported'.format(actual_schema, expected_schema))
        with closing(sqlite3.connect(database.as_uri() + '?mode=ro', uri=True)) as source:
            with closing(sqlite3.connect(str(backup / 'identity.db'))) as destination:
                source.backup(destination)
        with tempfile.NamedTemporaryFile(dir=target.parent, delete=False, prefix='.rttys-ci-') as output:
            staging = Path(output.name)
            try:
                with binary.open('rb') as source:
                    shutil.copyfileobj(source, output)
                os.fchmod(output.fileno(), stat.S_IMODE(old_info.st_mode))
                os.fchown(output.fileno(), old_info.st_uid, old_info.st_gid)
                output.flush()
                os.fsync(output.fileno())
                os.replace(staging, target)
                installed = True
            finally:
                staging.unlink(missing_ok=True)
        systemctl('start', config['service'])
        wait_ready(config, metadata['binary_sha256'])
        return dict(status='passed', source_commit=metadata['source_commit'], rollback='not_needed', backup=str(backup))
    except Exception as exc:
        result = dict(status='failed', error=str(exc), rollback='not_needed', backup=str(backup))
        if stopped:
            try:
                systemctl('stop', config['service'])
                # Never run an older binary against a changed schema or overwrite new user data.
                if installed and schema(database) != expected_schema:
                    raise RuntimeError('Schema changed; leave stopped for manual recovery from backup')
                with tempfile.NamedTemporaryFile(dir=target.parent, delete=False, prefix='.rttys-rollback-') as output:
                    restore = Path(output.name)
                try:
                    shutil.copy2(backup / 'rttys.previous', restore)
                    os.chown(restore, old_info.st_uid, old_info.st_gid)
                    os.replace(restore, target)
                finally:
                    restore.unlink(missing_ok=True)
                systemctl('start', config['service'])
                wait_ready(config, old_digest)
                result['rollback'] = 'passed'
            except Exception as rollback_error:
                result.update(rollback='failed', rollback_error=str(rollback_error))
        return result


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--package', required=True, type=Path)
    parser.add_argument('--sha256', required=True)
    parser.add_argument('--commit', required=True)
    parser.add_argument('--config', default='/etc/rttys/ci-deploy.json', type=Path)
    args = parser.parse_args()
    result = dict(status='failed', rollback='not_started')
    try:
        if os.geteuid() != 0:
            raise ValueError('Deployment requires root on the dedicated service host')
        os.umask(0o077)
        config = load_config(args.config)
        if not Path(config['binary']).is_file() or not Path(config['database']).is_file():
            raise ValueError('Existing service binary and database are required')
        root = Path(config['backup_root'])
        root.mkdir(parents=True, exist_ok=True, mode=0o700)
        if root.stat().st_uid != 0 or root.stat().st_mode & 0o077:
            raise ValueError('Backup root must be root-owned, mode 0700')
        with (root / 'deploy.lock').open('a') as lock, tempfile.TemporaryDirectory() as staging:
            fcntl.flock(lock, fcntl.LOCK_EX | fcntl.LOCK_NB)
            binary, metadata = unpack(args.package, args.sha256, args.commit, staging)
            backup = Path(tempfile.mkdtemp(prefix='release-' + args.commit[:12] + '-', dir=root))
            result = upgrade(config, binary, metadata, backup)
            (backup / 'result.json').write_text(json.dumps(result, indent=2) + '\n')
    except Exception as exc:
        result.update(error=str(exc))
    print(json.dumps(result))
    return 0 if result['status'] == 'passed' else 1


if __name__ == '__main__':
    raise SystemExit(main())
