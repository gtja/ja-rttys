import concurrent.futures
import json
from pathlib import Path
import subprocess
import tempfile
import unittest

ROOT = Path(__file__).resolve().parents[2]
ALLOCATOR = ROOT / 'ci/allocate-version.sh'


def next_version(base, tags, day):
    return subprocess.run(['bash', str(ALLOCATOR), '--next', base, day], input='\n'.join(tags) + '\n',
                          check=True, capture_output=True, text=True).stdout.strip()


class VersionTest(unittest.TestCase):
    def test_first_version_and_same_day_patch_increment(self):
        self.assertEqual(next_version('5.6.0', [], '261110'), 'v5.6.0.261110')
        tags = ['v5.6.0.261110', 'v5.6.1.261110', 'v5.6.40.261110-arm64', 'v8.0.99.261110']
        self.assertEqual(next_version('5.6.0', tags, '261110'), 'v5.6.2.261110')

    def test_next_day_carries_patch_and_base_version_can_advance(self):
        tags = ['v5.6.3.261110']
        self.assertEqual(next_version('5.6.0', tags, '261111'), 'v5.6.3.261111')
        self.assertEqual(next_version('5.6.8', tags, '261110'), 'v5.6.8.261110')
        self.assertEqual(next_version('5.7.0', tags, '261110'), 'v5.7.0.261110')

    def test_invalid_base_and_date_rejected(self):
        for base in ['v5.6.0', '5.6.0-tenant.4', '5.06.0', '5.6', '5.6.0\n']:
            with self.subTest(base=base), self.assertRaises(subprocess.CalledProcessError):
                next_version(base, [], '261110')
        with self.assertRaises(subprocess.CalledProcessError):
            next_version('5.6.0', [], '261332')

    def test_concurrent_same_commit_reservations_are_unique_and_preserve_source(self):
        with tempfile.TemporaryDirectory() as temp:
            temp = Path(temp)
            remote, work = temp / 'remote.git', temp / 'work'
            subprocess.run(['git', 'init', '--bare', str(remote)], check=True, capture_output=True)
            subprocess.run(['git', 'init', str(work)], check=True, capture_output=True)

            def git(*args):
                return subprocess.run(['git', *args], cwd=work, check=True, capture_output=True, text=True).stdout.strip()

            git('-c', 'user.name=CI Test', '-c', 'user.email=ci@example.test', 'commit', '--allow-empty', '-m', 'source')
            commit = git('rev-parse', 'HEAD')
            git('remote', 'add', 'origin', str(remote))
            git('push', 'origin', 'HEAD:refs/heads/dev')
            (work / 'VERSION').write_text('5.6.0\n')

            def reserve(index):
                output = temp / f'version-{index}.json'
                run = subprocess.run(['bash', str(ALLOCATOR), '--reserve',
                                      '--commit', commit, '--run-id', f'job-{index}', '--output', str(output)],
                                     cwd=work, check=True, capture_output=True, text=True)
                metadata = json.loads(output.read_text())
                self.assertTrue(metadata['reserved'])
                self.assertEqual(metadata['source_commit'], commit)
                self.assertEqual(metadata['time_zone'], 'Asia/Shanghai')
                return run.stdout.strip()

            with concurrent.futures.ThreadPoolExecutor(max_workers=2) as pool:
                reserved = list(pool.map(reserve, [1, 2]))
            self.assertEqual(len(set(reserved)), 2)
            first, second = sorted(reserved)
            self.assertTrue(first.startswith('v5.6.0.'))
            self.assertTrue(second.startswith('v5.6.1.'))
            for tag in reserved:
                self.assertEqual(git('--git-dir', str(remote), 'rev-parse', f'{tag}^{{commit}}'), commit)
            # Failed/reserved builds also consume their version; a third run cannot overwrite it.
            third = reserve(3)
            self.assertTrue(third.startswith('v5.6.2.'))
            self.assertEqual(len(git('--git-dir', str(remote), 'tag', '--list').splitlines()), 3)


if __name__ == '__main__':
    unittest.main()
