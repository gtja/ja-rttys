#!/usr/bin/env python3
"""Exercise OAuth and MCP against the disposable HTTPS server, never print tokens."""
import base64
import hashlib
import http.cookiejar
import json
import os
import pathlib
import secrets
import ssl
import urllib.error
import urllib.parse
import urllib.request

base = os.environ["RTTY_URL"]
resource = base + "/mcp"
client_id = "ja-rtty-codex"
# Only the local runner's disposable, self-signed HTTPS fixture uses this context.
context = ssl._create_unverified_context()


class NoRedirect(urllib.request.HTTPRedirectHandler):
    def redirect_request(self, req, fp, code, msg, headers, newurl):
        return None


def browser():
    return urllib.request.build_opener(
        NoRedirect(), urllib.request.HTTPCookieProcessor(http.cookiejar.CookieJar()),
        urllib.request.HTTPSHandler(context=context))


def request(client, method, path, value=None, form=False, bearer=None, expected=200):
    headers = {"Origin": base}
    data = None
    if value is not None:
        data = (urllib.parse.urlencode(value) if form else json.dumps(value)).encode()
        headers["Content-Type"] = "application/x-www-form-urlencoded" if form else "application/json"
    if bearer:
        headers.update({"Authorization": "Bearer " + bearer,
                        "Accept": "application/json, text/event-stream",
                        "MCP-Protocol-Version": "2025-11-25"})
    req = urllib.request.Request(base + path, data=data, headers=headers, method=method)
    try:
        response = client.open(req, timeout=10)
    except urllib.error.HTTPError as error:
        response = error
    with response:
        raw = response.read()
        if response.code != expected:
            raise AssertionError(f"{method} {path.split('?')[0]}: expected {expected}, got {response.code}")
        result = json.loads(raw) if raw and response.headers.get_content_type() == "application/json" else {}
        return result, response.headers


admin, user = browser(), browser()
suffix = secrets.token_hex(6)
password = "Local-MCP-fixture-password!" + suffix
request(admin, "POST", "/api/signin", {"username": "admin", "password": os.environ["RTTY_ADMIN_PASSWORD"]})
tenant, _ = request(admin, "POST", "/api/manage/tenants", {"name": "mcp-" + suffix}, expected=201)
request(admin, "POST", "/api/tenant", {"tenant_id": tenant["id"]})
account, _ = request(admin, "POST", "/api/manage/users", {"username": "mcp-" + suffix, "password": password}, expected=201)
request(admin, "PUT", "/api/manage/members", {"tenant_id": tenant["id"], "user_id": account["id"], "role": "operator"})
device, _ = request(admin, "POST", "/api/manage/devices", {"wire_id": "mcp-" + suffix, "name": "MCP fixture", "group": "", "web_targets": []}, expected=201)
request(admin, "PUT", "/api/manage/grants", {"user_id": account["id"], "device_id": device["id"], "group": "", "access": True})
request(admin, "PUT", "/api/manage/mcp-policies", {"user_id": account["id"], "scopes": ["devices:read"]}, expected=204)
request(user, "POST", "/api/signin", {"username": "mcp-" + suffix, "password": password})
metadata, _ = request(user, "GET", "/.well-known/oauth-protected-resource/mcp")
assert metadata["resource"] == resource
server, _ = request(user, "GET", "/.well-known/oauth-authorization-server")
assert server["issuer"] == base and server["authorization_response_iss_parameter_supported"]
verifier, state = secrets.token_urlsafe(48), secrets.token_urlsafe(32)
challenge = base64.urlsafe_b64encode(hashlib.sha256(verifier.encode()).digest()).rstrip(b"=").decode()
callback = "http://127.0.0.1:54321/callback"
query = urllib.parse.urlencode({"response_type": "code", "client_id": client_id,
    "redirect_uri": callback, "state": state, "scope": "devices:read",
    "resource": resource, "code_challenge": challenge, "code_challenge_method": "S256"})
_, headers = request(user, "GET", "/oauth/authorize?" + query, expected=302)
nonce = urllib.parse.parse_qs(urllib.parse.urlparse(headers["Location"]).query)["request"][0]
pending, _ = request(user, "GET", "/api/oauth/requests/" + nonce)
assert pending["client_id"] == client_id
consent, _ = request(user, "POST", "/api/oauth/authorize",
    {"request": nonce, "tenant_id": tenant["id"], "scopes": ["devices:read"], "approve": True})
values = urllib.parse.parse_qs(urllib.parse.urlparse(consent["redirect"]).query)
assert values["state"] == [state] and values["iss"] == [base]
token_form = {"grant_type": "authorization_code", "client_id": client_id,
    "code": values["code"][0], "code_verifier": verifier, "redirect_uri": callback, "resource": resource}
tokens, _ = request(user, "POST", "/oauth/token", token_form, form=True)
request(user, "POST", "/oauth/token", token_form, form=True, expected=400)
# A valid browser cookie must never authenticate a JSON-RPC request.
_, headers = request(user, "POST", "/mcp", {"jsonrpc": "2.0", "id": 1, "method": "tools/list"}, expected=401)
assert "resource_metadata=" in headers["WWW-Authenticate"]
initialized, _ = request(user, "POST", "/mcp", {"jsonrpc": "2.0", "id": 1, "method": "initialize",
    "params": {"protocolVersion": "2025-11-25", "capabilities": {}, "clientInfo": {"name": "https-e2e", "version": "1"}}},
    bearer=tokens["access_token"])
assert initialized["result"]["serverInfo"]["name"] == "ja-rtty"
tools, _ = request(user, "POST", "/mcp", {"jsonrpc": "2.0", "id": 2, "method": "tools/list"}, bearer=tokens["access_token"])
assert "run_diagnostic" in [item["name"] for item in tools["result"]["tools"]]
result, _ = request(user, "POST", "/mcp", {"jsonrpc": "2.0", "id": 3, "method": "tools/call",
    "params": {"name": "list_devices", "arguments": {}}}, bearer=tokens["access_token"])
devices = result["result"]["structuredContent"]["devices"]
assert len(devices) == 1 and devices[0]["id"] == device["id"] and "token" not in devices[0]
refresh_form = {"grant_type": "refresh_token", "client_id": client_id, "refresh_token": tokens["refresh_token"], "resource": resource}
new_tokens, _ = request(user, "POST", "/oauth/token", refresh_form, form=True)
assert new_tokens["access_token"] != tokens["access_token"]
request(user, "POST", "/oauth/token", refresh_form, form=True, expected=400)
request(user, "POST", "/mcp", {"jsonrpc": "2.0", "id": 4, "method": "tools/list"}, bearer=new_tokens["access_token"], expected=401)
summary = {"status": "passed", "transport": "real local HTTPS", "checks": [
    "administrator policy", "login and tenant consent", "discovery", "PKCE exchange",
    "authorization code replay rejection", "cookie rejected", "MCP initialize",
    "MCP tools/list", "device isolation", "refresh rotation", "refresh replay revokes grant"]}
if os.environ.get("RTTY_MCP_EVIDENCE"):
    pathlib.Path(os.environ["RTTY_MCP_EVIDENCE"]).write_text(json.dumps(summary, indent=2))
print(json.dumps(summary))
