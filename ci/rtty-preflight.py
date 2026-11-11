#!/usr/bin/env python3
"""Read service metadata without opening the locked SQLite database or writing files."""
import hashlib
import json
from pathlib import Path
import socket
import subprocess


def run(*args):
    return subprocess.check_output(args, text=True).strip()


def main():
    root = Path('/opt/ja-rttys')
    properties = dict(line.split('=', 1) for line in run(
        'systemctl', 'show', 'ja-rttys.service', '--property=ActiveState,MainPID,User').splitlines())
    assert properties['ActiveState'] == 'active', 'rtty service is not active'
    pid = int(properties['MainPID'])
    assert pid > 0
    binary = root / 'rttys'
    assert Path('/proc/{}/exe'.format(pid)).resolve() == binary, 'unexpected running executable'
    assert str(root / 'data/identity.db') in [p.resolve().as_posix() for p in Path('/proc/{}/fd'.format(pid)).glob('*')], 'unexpected database'
    with (root / 'data/identity.db').open('rb') as stream:
        header = stream.read(100)
    assert header[:16] == b'SQLite format 3\0', 'invalid SQLite header'
    print(json.dumps(dict(status='passed', host=socket.gethostname(), service='ja-rttys.service',
                         state=properties['ActiveState'], user=properties['User'], pid=pid,
                         version=run(str(binary), '--version'),
                         binary_sha256=hashlib.sha256(binary.read_bytes()).hexdigest(),
                         identity_schema=int.from_bytes(header[60:64], 'big'),
                         deploy_configured=Path('/etc/ja-rttys/ci-deploy.json').is_file())))


if __name__ == '__main__':
    main()
