<template>
  <div class="mcp-policy">
    <template v-if="available"><h3>MCP 访问权限</h3><p>{{scopes.length ? scopes.map(scopeLabel).join('、') : '未开放 MCP 访问'}}</p><el-button @click="edit">设置 MCP 权限</el-button></template>
    <el-dialog v-model="open" title="设置 MCP 权限" width="min(540px, 94vw)" append-to-body>
      <p>账号：{{member.username}} · 权限仅作用于当前租户。</p>
      <p>设备范围沿用成员的设备、分组授权。清空以下选项即可禁止 MCP 访问。</p>
      <el-checkbox-group v-model="selected" class="policy-scopes"><el-checkbox v-for="item in mcpScopes" :key="item.value" :value="item.value" :disabled="member.role==='viewer' && item.value!=='devices:read'"><span><b>{{item.label}}</b><small>{{item.description}}</small></span></el-checkbox></el-checkbox-group>
      <el-alert title="保存后，该成员在当前租户的已有 MCP 授权将撤销，终端将关闭，需要重新登录授权。" type="warning" :closable="false"/>
      <el-alert v-if="error" :title="error" type="error" :closable="false"/>
      <template #footer><el-button @click="open=false">取消</el-button><el-button type="primary" :loading="busy" @click="save">保存权限</el-button></template>
    </el-dialog>
  </div>
</template>
<script setup>
import { ref, onMounted } from 'vue'
import axios from 'axios'
import { mcpScopes, scopeLabel } from '../mcpScopes'
const props = defineProps({member:{type:Object,required:true}})
const available = ref(false), scopes = ref([]), selected = ref([]), open = ref(false), busy = ref(false), error = ref('')
async function load() {
  try { const {data} = await axios.get('/api/manage/mcp-policies'); if (!Array.isArray(data)) return; available.value = true; scopes.value = data.find(item => item.user_id === props.member.id)?.scopes || [] }
  catch { available.value = false }
}
async function edit() { await load(); if (!available.value) return; selected.value = [...scopes.value]; error.value = ''; open.value = true }
async function save() {
  busy.value = true; error.value = ''
  try { await axios.put('/api/manage/mcp-policies',{user_id:props.member.id,scopes:selected.value}); scopes.value = [...selected.value]; open.value = false }
  catch { error.value = '权限未保存，请检查当前租户、成员角色和管理员权限。' }
  finally { busy.value = false }
}
onMounted(load)
</script>
<style scoped>
.mcp-policy{margin:20px 0}.mcp-policy p{color:#65718a;font-size:13px;line-height:1.7}.policy-scopes{display:flex;flex-direction:column;gap:20px;margin:24px 0}.policy-scopes :deep(.el-checkbox){height:auto;white-space:normal;align-items:flex-start;margin:0}.policy-scopes :deep(.el-checkbox__label){white-space:normal}.policy-scopes b,.policy-scopes small{display:block;line-height:1.7}
</style>
