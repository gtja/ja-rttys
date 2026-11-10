#!/usr/bin/env python3
"""Allocate dated versions using atomic, append-only Git tag creation."""
import argparse
from datetime import datetime, timedelta, timezone
import json
from pathlib import Path
import re
import subprocess
import time

SEMVER = re.compile(r'(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)')
VERSION = re.compile(r'v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.([0-9]{6})')
CHINA = timezone(timedelta(hours=8), 'Asia/Shanghai')


def next_version(base, tags, day):
    match = SEMVER.fullmatch(base)
    if not match:
        raise ValueError('VERSION must contain major.minor.patch without a prefix or suffix')
    datetime.strptime(day, '%y%m%d')
    major, minor, floor = map(int, match.groups())
    previous = []
    for tag in tags:
        found = VERSION.fullmatch(tag)
        if found and tuple(map(int, found.groups()[:2])) == (major, minor):
            previous.append((int(found[3]), found[4]))
    patch = max([floor] + [p for p, _ in previous])
    if (patch, day) in previous:
        patch += 1
    return f'v{major}.{minor}.{patch}.{day}'


def git(*args, input=None, check=True):
    return subprocess.run(['git', *args], input=input, text=True,
                          stdout=subprocess.PIPE, stderr=subprocess.PIPE, check=check)


def remote_tags(remote):
    result = git('ls-remote', '--tags', '--refs', remote)
    return [line.split('\trefs/tags/', 1)[1] for line in result.stdout.splitlines()]


def allocate(base, remote, commit, run_id, reserve, day=None):
    if not re.fullmatch(r'[0-9a-f]{40}', commit) or not run_id or '\n' in run_id:
        raise ValueError('A selected commit and single-line CI run identity are required')
    day = day or datetime.now(CHINA).strftime('%y%m%d')
    for _ in range(20):
        tags = remote_tags(remote) if reserve else git('tag', '--list').stdout.splitlines()
        version = next_version(base, tags, day)
        tag_object = None
        if reserve:
            tag = (f'object {commit}\ntype commit\ntag {version}\n'
                   f'tagger Jenkins CI <ci@jingan.com> {int(time.time())} +0800\n\n'
                   f'CI version reservation: {run_id}\n'
                   'A reserved version is not proof of a successful image publication.\n')
            tag_object = git('mktag', input=tag).stdout.strip()
            pushed = git('push', remote, f'{tag_object}:refs/tags/{version}', check=False)
            if pushed.returncode:
                # A competing job may have atomically created this tag first.
                if version in remote_tags(remote):
                    continue
                raise RuntimeError('Unable to reserve version: ' + pushed.stderr)
        return dict(version=version, base_version=base, build_date=day,
                    time_zone='Asia/Shanghai', source_commit=commit, ci_run_id=run_id,
                    reserved=reserve, git_tag=version if reserve else None,
                    tag_object=tag_object)
    raise RuntimeError('Too many concurrent version reservations; retry the build')


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--base-file', default='VERSION')
    parser.add_argument('--remote', default='origin')
    parser.add_argument('--commit', required=True)
    parser.add_argument('--run-id', required=True)
    parser.add_argument('--reserve', action='store_true')
    parser.add_argument('--output', default='out/source/version.json')
    args = parser.parse_args()
    metadata = allocate(Path(args.base_file).read_text().strip(), args.remote,
                        args.commit, args.run_id, args.reserve)
    output = Path(args.output)
    output.parent.mkdir(parents=True, exist_ok=True)
    output.write_text(json.dumps(metadata, indent=2) + '\n')
    print(metadata['version'])


if __name__ == '__main__':
    main()
