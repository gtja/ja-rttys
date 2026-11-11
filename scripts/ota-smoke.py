#!/usr/bin/env python3
"""Read-only deployed OTA authorization smoke. Does not prove device OTA."""
import argparse
import http.cookiejar
import json
import ssl
import urllib.error
import urllib.request
from pathlib import Path

p = argparse.ArgumentParser()
p.add_argument('--fixture', required=True)
p.add_argument('--ca', required=True)
p.add_argument('--output', required=True)
a = p.parse_args()
f = json.loads(Path(a.fixture).read_text())
base = f['url'].rstrip('/')
records = []

def client():
    return urllib.request.build_opener(urllib.request.HTTPCookieProcessor(http.cookiejar.CookieJar()), urllib.request.HTTPSHandler(context=ssl.create_default_context(cafile=a.ca)))

def request(c, method, path, body=None):
    req = urllib.request.Request(base + path, data=None if body is None else json.dumps(body).encode(), method=method, headers={'Content-Type': 'application/json', 'Origin': base})
    try:
        r = c.open(req, timeout=20)
    except urllib.error.HTTPError as e:
        r = e
    with r:
        raw = r.read()
        try:
            body = json.loads(raw)
        except ValueError:
            body = None
        return r.code, body

def check(name, actual, expected):
    records.append(dict(check=name, actual=actual, expected=expected, passed=actual == expected))
    Path(a.output).write_text(json.dumps({'url': base, 'checks': records, 'unverified': ['174 enrollment', 'application upgrade', 'failed upgrade rollback', 'browser interactions']}, indent=2))
    print(name, actual == expected)
    assert actual == expected, name

check('anonymous denied', request(client(), 'GET', '/api/ota')[0], 401)
for name in ['tenant-admin', 'operator', 'viewer', 'outsider']:
    c = client()
    check(name + ' login', request(c, 'POST', '/api/signin', {'username': name, 'password': f['users'][name]['password']})[0], 200)
    code, body = request(c, 'GET', '/api/ota')
    check(name + ' overview status', code, 403 if name in ['operator', 'viewer'] else 200)
    if name == 'tenant-admin':
        check('Mender bridge enabled', body['enabled'], True)
        check('no unverified device bindings', body['devices'], [])
        check('unknown task hidden', request(c, 'GET', '/api/ota/deployments/nonexistent')[0], 404)
    elif name == 'outsider':
        check('unconfigured tenant isolated', body, {'enabled': False})
