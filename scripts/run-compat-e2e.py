#!/usr/bin/env python3
"""Exercise the production feature set on disposable synthetic fixtures."""
import json
import os
from pathlib import Path
import subprocess

output = Path(os.environ.get('RTTY_COMPAT_EVIDENCE', '/ci'))
output.mkdir(parents=True, exist_ok=True)
results = []
for name in ['device-auth-mode', 'admin-overview', 'device-edit-features', 'presence']:
    log = output / (name + '-e2e.log')
    with log.open('w') as stream:
        result = subprocess.run(['python3', 'scripts/' + name + '-e2e.py'],
                                stdout=stream, stderr=subprocess.STDOUT, timeout=180)
    results.append(dict(suite=name, exit_code=result.returncode))
    (output / 'compat-e2e.json').write_text(json.dumps(results, indent=2) + '\n')
    if result.returncode:
        print(log.read_text(), flush=True)
        raise SystemExit(result.returncode)
print(json.dumps(results))
