// Only these local destinations may survive a login. The OAuth callback must
// never become the console's own login redirect.
export function loginDestination(value) {
  if (typeof value !== 'string') return '/'
  if (value === '/mcp-access') return value
  if (/^\/oauth\/consent\?request=[a-f0-9]{64}$/.test(value)) return value
  return '/'
}
