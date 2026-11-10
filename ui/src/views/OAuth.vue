<template>
  <main class="oauth-page"><section class="oauth-card" v-loading="loading">
    <router-link to="/">RTTY 控制台</router-link>
    <h1>授权 MCP 访问</h1>
    <el-alert v-if="error" :title="error" type="error" :closable="false" />
    <template v-if="request">
      <p>客户端 <strong>{{request.client_id}}</strong> 请求以 <strong>{{me.user?.username}}</strong> 的身份访问设备。</p>
      <el-form label-position="top"><el-form-item label="授权工作空间"><el-select v-model="tenant" aria-label="授权租户"><el-option v-for="item in me.tenants" :key="item.id" :label="item.name" :value="item.id" /></el-select></el-form-item></el-form>
      <p class="hint">权限由管理员设置，设备范围沿用此账号的设备和分组授权。此次授权最长有效 24 小时，可随时在“MCP 授权”中撤销。</p>
      <el-checkbox-group v-model="selected" class="scope-options">
        <el-checkbox v-for="scope in requestedScopes" :key="scope.value" :value="scope.value" :disabled="!allowed.includes(scope.value)"><span><b>{{scope.label}}</b><small>{{scope.description}}</small><small v-if="!allowed.includes(scope.value)">当前工作空间的管理员未开放此权限。</small></span></el-checkbox>
      </el-checkbox-group>
      <el-alert v-if="selected.includes('terminal:interactive')" title="交互终端允许执行命令、修改和删除设备上的数据。请仅授权你信任的客户端。" type="warning" :closable="false" />
      <p v-if="!allowed.length" class="hint">当前工作空间未开放请求的 MCP 权限，请联系管理员。</p>
      <div class="oauth-actions"><el-button :disabled="busy" @click="submit(false)">拒绝授权</el-button><el-button type="primary" :loading="busy" :disabled="!selected.length" @click="submit(true)">同意并继续</el-button></div>
    </template>
  </section></main>
</template>

<script setup>
import { ref, computed, watch, onMounted } from 'vue'
import { useRoute } from 'vue-router'
import axios from 'axios'
import { mcpScopes, permittedScopes } from '../mcpScopes'
const route = useRoute(), request = ref(null), me = ref({tenants:[]}), tenant = ref(''), selected = ref([]), error = ref(''), loading = ref(true), busy = ref(false)
const requestedScopes = computed(() => mcpScopes.filter(scope => request.value?.scopes.includes(scope.value)))
const allowed = computed(() => permittedScopes(request.value?.scopes || [], request.value?.policies[tenant.value] || [], me.value.tenants.find(item => item.id === tenant.value)?.role))
watch(allowed, values => { selected.value = values.filter(value => value !== 'terminal:interactive') })
onMounted(async () => {
  try {
    if (!/^[a-f0-9]{64}$/.test(route.query.request || '')) throw new Error()
    const [account, pending] = await Promise.all([axios.get('/api/me'), axios.get('/api/oauth/requests/' + route.query.request)])
    me.value = account.data; request.value = pending.data
    tenant.value = me.value.user.tenant_id || me.value.tenants[0]?.id || ''
  } catch { error.value = '授权请求无效、已过期，或当前会话无法访问。请在客户端重新发起登录。' }
  finally { loading.value = false }
})
async function submit(approve) {
  if (busy.value) return
  busy.value = true; error.value = ''
  try {
    const {data} = await axios.post('/api/oauth/authorize', {request:route.query.request, tenant_id:tenant.value, scopes:selected.value, approve})
    window.location.assign(data.redirect)
  } catch { error.value = '授权未完成，请检查管理员权限设置，或在客户端重新发起登录。'; busy.value = false }
}
</script>
<style scoped>
.oauth-page{min-height:100dvh;background:#f4f6fb;padding:48px 20px;color:#273047;box-sizing:border-box}.oauth-card{max-width:620px;margin:auto;background:white;border:1px solid #e2e6f0;border-radius:16px;padding:32px}h1{font-size:26px;margin:26px 0}p{line-height:1.8}.hint{font-size:13px;color:#65718a}.scope-options{display:flex;flex-direction:column;gap:20px;margin:24px 0}.scope-options :deep(.el-checkbox){height:auto;white-space:normal;align-items:flex-start;margin:0}.scope-options :deep(.el-checkbox__label){white-space:normal}.scope-options b{display:block;font-size:15px}.scope-options small{display:block;margin-top:6px;line-height:1.6}.oauth-actions{display:flex;justify-content:flex-end;gap:12px;margin-top:28px}.el-select{width:100%}a{color:#455cd0}
</style>
