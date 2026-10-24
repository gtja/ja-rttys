#!/usr/bin/env python3
"""Create auditable binary bundles; never include the live database or secrets."""
import hashlib
import json
import os
from pathlib import Path
import sys
import tarfile


def sha256(path):
    with Path(path).open('rb') as stream:
        digest = hashlib.sha256()
        for chunk in iter(lambda: stream.read(1024 * 1024), b''):
            digest.update(chunk)
        return digest.hexdigest()


def bundle(directory, version, commit, arch, image, image_id):
    directory = Path(directory)
    with (directory / 'rttys').open('rb') as source:
        header = source.read(20)
    if header[:6] != b'\x7fELF\x02\x01' or int.from_bytes(header[18:20], 'little') != {'amd64': 62, 'arm64': 183}.get(arch):
        raise ValueError('Binary ELF architecture does not match the selected target')
    metadata = dict(service='ja-rttys', version=version, source_commit=commit,
                    architecture=arch, identity_schema=2, image=image,
                    image_id=image_id, binary_sha256=sha256(directory / 'rttys'),
                    pushed=False)
    (directory / 'manifest.json').write_text(json.dumps(metadata, indent=2) + '\n')
    archive = directory / f'ja-rttys-{version}-linux-{arch}.tar.gz'
    with tarfile.open(archive, 'w:gz') as tar:
        tar.add(directory / 'rttys', arcname='rttys')
        tar.add(directory / 'manifest.json', arcname='manifest.json')
        for path in ['deploy/rttys.conf', 'deploy/rttys.service', 'README-TENANT.md']:
            tar.add(path, arcname=Path(path).name)
    (directory / (archive.name + '.sha256')).write_text(sha256(archive) + '  ' + archive.name + '\n')
    return metadata


if __name__ == '__main__':
    directory = Path(os.environ['RELEASE_DIR'])
    if sys.argv[1:] == ['--published']:
        path = directory / 'image.json'
        metadata = json.loads(path.read_text())
        metadata.update(pushed=True, digest=os.environ['IMAGE_DIGEST'])
    elif not sys.argv[1:]:
        metadata = bundle(directory, os.environ['PACKAGE_VERSION'], os.environ['SOURCE_COMMIT'],
                          os.environ['TARGET_ARCH'], os.environ['IMAGE_NAME'], os.environ['IMAGE_ID'])
    else:
        raise SystemExit('Usage: ci-package.py [--published]')
    (directory / 'image.json').write_text(json.dumps(metadata, indent=2) + '\n')
