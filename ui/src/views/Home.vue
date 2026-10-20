<template>
 <main class="console">
  <header><div><h1>RTTY 远程运维</h1><p>{{me.user?.username}} · {{me.user?.platform_admin?'平台管理员':me.user?.role}}</p></div>
   <el-space><el-select aria-label="当前租户" v-model="tenant" @change="switchTenant" placeholder="选择租户" style="width:220px"><el-option v-for="t in me.tenants" :key="t.id" :label="t.name" :value="t.id"/></el-select><el-button @click="refresh">刷新</el-button><el-button @click="passwordDialog=true">修改密码</el-button><el-button @click="logout">退出</el-button></el-space>
  </header>
  <el-alert v-if="error" :title="error" type="error" :closable="false"/>
  <el-tabs v-model="tab">
   <el-tab-pane label="设备" name="devices">
    <el-input v-model="search" placeholder="筛选设备编号、名称或分组" clearable style="max-width:420px"/>
    <el-table :data="visibleDevices" empty-text="当前租户没有可访问设备">
     <el-table-column prop="wire_id" label="设备编号"/><el-table-column prop="description" label="名称"/><el-table-column prop="group" label="设备组"/>
     <el-table-column label="状态"><template #default="{row}"><el-tag :type="row.online?'success':'info'">{{row.online?'在线':'离线'}}</el-tag></template></el-table-column>
     <el-table-column label="操作"><template #default="{row}"><el-button :disabled="!row.online||!row.can_operate" @click="connect(row)">打开终端</el-button><el-button :disabled="!row.online||!row.can_operate" @click="web.dev=row;web.modal=true">设备 Web</el-button></template></el-table-column>
    </el-table><p class="hint">终端保留原有文件传输功能。系统账号权限由设备操作系统管理。</p>
   </el-tab-pane>
   <el-tab-pane v-if="manager" label="管理" name="manage">
    <section v-if="me.user?.platform_admin"><h2>租户</h2><el-space><el-input v-model="newTenant" placeholder="租户名称"/><el-button @click="createTenant">创建租户</el-button></el-space>
     <el-table :data="snapshot.tenants"><el-table-column prop="name" label="名称"/><el-table-column label="操作"><template #default="{row}"><el-button @click="changeDisabled('tenants',row)">{{row.disabled?'启用':'禁用并断开'}}</el-button></template></el-table-column></el-table>
    </section>
    <section><h2>账号与成员</h2><el-space wrap><el-input v-model="account.username" placeholder="新用户名"/><el-input v-model="account.password" type="password" show-password placeholder="密码，12–72字节"/><el-select v-model="account.role" style="width:140px"><el-option label="租户管理员" value="admin"/><el-option label="运维成员" value="operator"/><el-option label="查看者" value="viewer"/></el-select><el-button @click="createUser">创建并加入当前租户</el-button></el-space>
     <el-space v-if="me.user?.platform_admin" wrap class="row"><el-select v-model="member.user_id" placeholder="已有账号" style="width:200px"><el-option v-for="u in snapshot.users" :key="u.id" :label="u.username" :value="u.id"/></el-select><el-select v-model="member.role" style="width:140px"><el-option label="租户管理员" value="admin"/><el-option label="运维成员" value="operator"/><el-option label="查看者" value="viewer"/></el-select><el-button @click="addMember">加入当前租户</el-button></el-space>
     <el-table :data="snapshot.members"><el-table-column prop="username" label="成员"/><el-table-column prop="role" label="角色"/><el-table-column label="操作"><template #default="{row}"><el-button :disabled="row.id===me.user?.id" @click="removeMember(row)">移除并撤销会话</el-button></template></el-table-column></el-table>
     <el-table v-if="me.user?.platform_admin" :data="snapshot.users"><el-table-column prop="username" label="平台账号"/><el-table-column label="操作"><template #default="{row}"><el-button :disabled="row.id===me.user?.id" @click="changeDisabled('users',row)">{{row.disabled?'启用':'禁用账号'}}</el-button></template></el-table-column></el-table>
    </section>
    <section><h2>登记设备</h2><el-space wrap><el-input v-model="device.wire_id" placeholder="rtty设备编号（-I）"/><el-input v-model="device.name" placeholder="显示名称"/><el-input v-model="device.group" placeholder="设备组"/><el-input v-model="webTargets" placeholder="允许的Web目标，逗号分隔" style="width:320px"/><el-button :disabled="!tenant" @click="enroll">登记并签发凭据</el-button></el-space>
     <el-alert v-if="credential" title="凭据仅在本次操作显示，请安全保存" type="warning" :closable="false"/><pre v-if="credential" class="credential">{{credential}}</pre><el-button v-if="credential" @click="credential=''">清除显示</el-button>
     <el-table :data="snapshot.devices"><el-table-column prop="wire_id" label="设备编号"/><el-table-column prop="group" label="设备组"/><el-table-column label="操作"><template #default="{row}"><el-button @click="rotate(row)">轮换并断开</el-button><el-button @click="changeDisabled('devices',row)">{{row.disabled?'启用':'禁用并断开'}}</el-button></template></el-table-column></el-table>
    </section>
    <section><h2>运维成员授权</h2><el-space wrap><el-select v-model="grant.user_id" placeholder="成员" style="width:170px"><el-option v-for="u in snapshot.members" :key="u.id" :label="u.username" :value="u.id"/></el-select><el-select v-model="grant.device_id" clearable placeholder="单台设备（可留空）" style="width:220px"><el-option v-for="d in snapshot.devices" :key="d.id" :label="d.wire_id" :value="d.id"/></el-select><el-input v-model="grant.group" :disabled="!!grant.device_id" placeholder="或填写设备组"/><el-button @click="changeGrant(true)">授权</el-button><el-button @click="changeGrant(false)">撤销</el-button></el-space>
     <el-table :data="grantRows"><el-table-column prop="username" label="成员"/><el-table-column prop="target" label="授权范围"/></el-table><p class="hint">查看者只能查看。运维成员需有设备或设备组授权。租户管理员管理本租户全部设备。</p>
    </section>
    <section><h2>最近管理记录</h2><el-table :data="snapshot.audit"><el-table-column prop="at" label="时间戳"/><el-table-column prop="action" label="操作"/><el-table-column prop="resource" label="资源"/></el-table></section>
   </el-tab-pane>
  </el-tabs><RttyWeb v-model="web.modal" :dev="web.dev"/>
  <el-dialog v-model="passwordDialog" title="修改密码" width="400px"><el-form @submit.prevent="changePassword"><el-form-item label="当前密码"><el-input type="password" v-model="password.current_password" autocomplete="current-password"/></el-form-item><el-form-item label="新密码"><el-input type="password" v-model="password.new_password" autocomplete="new-password" placeholder="12–72字节"/></el-form-item><el-button type="primary" @click="changePassword">修改并重新登录</el-button></el-form></el-dialog>
 </main>
</template>
<script setup>
import {ref,reactive,computed,onMounted} from 'vue'
import axios from 'axios'
import {useRouter} from 'vue-router'
import RttyWeb from '../components/RttyWeb.vue'
const router=useRouter(),me=ref({tenants:[]}),tenant=ref(''),devices=ref([]),snapshot=ref({}),error=ref(''),tab=ref('devices'),search=ref(''),newTenant=ref(''),credential=ref(''),webTargets=ref('')
const account=reactive({username:'',password:'',role:'operator'}),member=reactive({user_id:'',role:'operator'}),device=reactive({wire_id:'',name:'',group:''}),grant=reactive({user_id:'',device_id:'',group:''}),web=reactive({modal:false,dev:null})
const passwordDialog=ref(false),password=reactive({current_password:'',new_password:''})
const manager=computed(()=>me.value.user?.platform_admin||me.value.user?.role==='admin')
const visibleDevices=computed(()=>devices.value.filter(d=>`${d.wire_id} ${d.description} ${d.group}`.toLowerCase().includes(search.value.toLowerCase())))
const grantRows=computed(()=>(snapshot.value.grants||[]).map(g=>({username:snapshot.value.members?.find(u=>u.id===g.user_id)?.username||g.user_id,target:g.device_id?(snapshot.value.devices?.find(d=>d.id===g.device_id)?.wire_id||g.device_id):`设备组：${g.group}`})))
async function refresh(){try{me.value=(await axios.get('/api/me')).data;tenant.value=me.value.user.tenant_id;devices.value=(await axios.get('/api/devs')).data;snapshot.value=manager.value?(await axios.get('/api/manage/snapshot')).data:{}}catch(e){if(e.response?.status===401)router.push('/login');else error.value='加载失败，请重试'}}
async function action(fn){error.value='';try{await fn();await refresh()}catch(e){error.value=e.response?.data?.error||`操作未完成（${e.response?.status||'网络错误'}）`}}
async function switchTenant(){credential.value='';await action(()=>axios.post('/api/tenant',{tenant_id:tenant.value}))}
async function logout(){await axios.post('/api/signout',{});router.push('/login')}
async function changePassword(){try{await axios.post('/api/password',password);password.current_password='';password.new_password='';passwordDialog.value=false;router.push('/login')}catch(e){error.value=`修改密码失败（${e.response?.status||'网络错误'}）`;passwordDialog.value=false}}
function connect(d){window.open(`/rtty/${encodeURIComponent(d.id)}`,'_blank','noopener')}
async function createTenant(){await action(async()=>{const {data}=await axios.post('/api/manage/tenants',{name:newTenant.value});newTenant.value='';await axios.post('/api/tenant',{tenant_id:data.id})})}
async function createUser(){await action(async()=>{const {data}=await axios.post('/api/manage/users',{username:account.username,password:account.password});account.password='';if(tenant.value)await axios.put('/api/manage/members',{user_id:data.id,tenant_id:tenant.value,role:account.role});account.username=''})}
async function addMember(){await action(()=>axios.put('/api/manage/members',{...member,tenant_id:tenant.value}))}
async function removeMember(u){await action(()=>axios.delete(`/api/manage/members/${u.id}`))}
async function changeDisabled(kind,row){await action(()=>axios.patch(`/api/manage/${kind}/${row.id}`,{disabled:!row.disabled}))}
async function enroll(){await action(async()=>{const {data}=await axios.post('/api/manage/devices',{...device,web_targets:webTargets.value.split(',').map(s=>s.trim()).filter(Boolean)});credential.value=`设备编号：${data.wire_id}\nToken：${data.token}`})}
async function rotate(d){await action(async()=>{const {data}=await axios.post(`/api/manage/devices/${d.id}/rotate`,{});credential.value=`设备编号：${d.wire_id}\n新 Token：${data.token}`})}
async function changeGrant(access){await action(()=>axios.put('/api/manage/grants',{...grant,access}))}
onMounted(refresh)
</script>
<style scoped>
.console{max-width:1440px;margin:0 auto;padding:24px;font-family:system-ui,sans-serif}header{display:flex;justify-content:space-between;align-items:center;gap:20px;margin-bottom:24px}h1{font-size:24px;margin:0}header p,.hint{color:#667085;font-size:13px}section{border:1px solid #dce2e8;border-radius:8px;padding:20px;margin:20px 0}h2{font-size:17px;margin:0 0 16px}.row{margin:16px 0}.credential{white-space:pre-wrap;overflow-wrap:anywhere;background:#fff7dc;padding:16px}.el-table{margin-top:16px}@media(max-width:800px){header{align-items:flex-start;flex-direction:column}.console{padding:12px}}
</style>
