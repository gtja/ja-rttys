export const mcpScopes = [
  { value: 'devices:read', label: '查看设备列表', description: '查看当前账号有权访问的设备信息。' },
  { value: 'diagnostics:read', label: '执行诊断查询', description: '以配置的非 root 系统账号查询状态、进程和指定服务日志，仍需设备操作权限。' },
  { value: 'terminal:interactive', label: '使用交互终端', description: '可输入命令并修改设备，仍需设备操作权限。' }
]
export function scopeLabel(scope) { return mcpScopes.find(item => item.value === scope)?.label || scope }
export function permittedScopes(requested, policy, role) {
  return requested.filter(scope => mcpScopes.some(item => item.value === scope) && policy.includes(scope) && (role !== 'viewer' || scope === 'devices:read'))
}
