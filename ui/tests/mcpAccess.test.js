import { test } from 'node:test'
import assert from 'node:assert/strict'
import { loginDestination } from '../src/loginRedirect.js'
import { permittedScopes } from '../src/mcpScopes.js'

test('login returns to a pending consent without accepting an external redirect', () => {
  const consent = '/oauth/consent?request=' + 'a'.repeat(64)
  assert.equal(loginDestination(consent), consent)
  assert.equal(loginDestination('/mcp-access'), '/mcp-access')
  for (const value of ['https://evil.test', '//evil.test', '/\\evil.test', '/oauth/consent?request=x', consent + '&redirect=https://evil.test', [consent], undefined]) {
    assert.equal(loginDestination(value), '/')
  }
})

test('consent permissions intersect the request, administrator policy and member role', () => {
  const requested = ['devices:read', 'diagnostics:read', 'terminal:interactive']
  assert.deepEqual(permittedScopes(requested, requested, 'viewer'), ['devices:read'])
  assert.deepEqual(permittedScopes(requested, ['devices:read'], 'operator'), ['devices:read'])
  assert.deepEqual(permittedScopes(requested, [], 'admin'), [])
  assert.deepEqual(permittedScopes(['devices:read'], requested, 'admin'), ['devices:read'])
})
