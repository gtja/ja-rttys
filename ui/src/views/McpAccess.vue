<template>
  <main class="access-page"><router-link to="/">返回工作台</router-link><h1>MCP 授权</h1><p>管理已授权的客户端。撤销授权会使对应令牌失效，并关闭其正在使用的终端。</p>
    <el-alert v-if="error" :title="error" type="error" :closable="false" />
    <section v-if="info" class="access-panel"><h2>连接 Codex</h2><p>先由租户管理员开放 MCP 权限，再在 Codex 中添加服务并登录。</p><pre>{{addCommand}}
codex mcp login ja-rtty --scopes devices:read,diagnostics:read,terminal:interactive</pre><p>登录后选择租户并确认权限。交互终端需要单独勾选。</p></section>
    <el-table v-loading="loading" :data="info?.grants || []" empty-text="暂无授权"><el-table-column prop="client_id" label="客户端" /><el-table-column label="租户"><template #default="{row}">{{tenantNames[row.tenant_id] || row.tenant_id}}</template></el-table-column><el-table-column label="权限" min-width="210"><template #default="{row}">{{row.scopes.map(scopeLabel).join('、')}}</template></el-table-column><el-table-column label="有效期" min-width="180"><template #default="{row}">{{new Date(row.expires*1000).toLocaleString()}}</template></el-table-column><el-table-column label="状态"><template #default="{row}">{{row.revoked?'已撤销':row.expires*1000<=Date.now()?'已过期':'有效'}}</template></el-table-column><el-table-column width="100"><template #default="{row}"><el-button text type="danger" :disabled="row.revoked || busy" @click="revoke(row)">撤销</el-button></template></el-table-column></el-table>
  </main>
</template>
<script setup>
import { ref, computed, onMounted } from 'vue'
import axios from 'axios'
import { scopeLabel } from '../mcpScopes'
const info = ref(null), tenantNames = ref({}), error = ref(''), loading = ref(true), busy = ref(false)
const quote = value => "'" + value.replaceAll("'", "'\\''") + "'"
const addCommand = computed(() => info.value ? 'codex mcp add ja-rtty --url ' + quote(info.value.url) + ' --oauth-client-id ' + quote(info.value.client_id) + ' --oauth-resource ' + quote(info.value.url) : '')
async function load() {
  try { const [grants, me] = await Promise.all([axios.get('/api/oauth/grants'), axios.get('/api/me')]); if (!Array.isArray(grants.data.grants)) throw new Error(); info.value = grants.data; tenantNames.value = Object.fromEntries(me.data.tenants.map(item => [item.id,item.name])) }
  catch (e) { error.value = e.response?.status === 401 ? '登录已失效，请返回工作台重新登录。' : '当前服务未启用 MCP，或暂时无法加载授权。' }
  finally { loading.value = false }
}
async function revoke(row) { busy.value = true; error.value = ''; try { await axios.delete('/api/oauth/grants/' + row.id); await load() } catch { error.value = '撤销失败，请重试。' } finally { busy.value = false } }
onMounted(load)
</script>
<style scoped>
.access-page{max-width:1200px;margin:40px auto;padding:0 24px;color:#273047}.access-page>p{color:#65718a;line-height:1.7}.access-panel{background:#f4f6fb;border-radius:12px;padding:20px 24px;margin:24px 0}h1{font-size:28px}h2{font-size:18px}pre{overflow:auto;white-space:pre-wrap;word-break:break-word;font-size:13px;line-height:1.8}a{color:#455cd0}
</style>
