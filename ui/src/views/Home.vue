<template>
 <div class="workspace">
  <a class="skip-link" href="#workspace-content">跳到主要内容</a>
  <aside class="sidebar">
   <div class="brand"><span class="brand-mark">›_</span><div>RTTY<span class="brand-sub">远程运维控制台</span></div></div>
   <div class="nav-caption">工作空间</div>
   <nav aria-label="主导航">
    <button v-for="item in navigation" :key="item.key" :class="['nav-item',{active:tab===item.key}]" :aria-current="tab===item.key?'page':undefined" @click="tab=item.key;error=''"><span class="nav-symbol" aria-hidden="true">{{item.icon}}</span><span>{{item.title}}</span><span v-if="item.key==='devices'" class="nav-count">{{devices.length}}</span></button>
   </nav>
   <div class="sidebar-note"><span>访问边界</span><p>设备与权限按租户隔离。<br>操作范围由当前账号授权决定。</p></div>
   <div class="profile"><span class="avatar">{{me.user?.username?.slice(0,1).toUpperCase()||'R'}}</span><div><strong>{{me.user?.username||'正在加载'}}</strong><small>{{me.user?.platform_admin?'平台管理员':roleLabel(me.user?.role)}}</small></div><button class="logout" @click="logout">退出</button></div>
  </aside>
  <div class="main-shell">
   <header class="topbar"><div class="breadcrumb">工作空间 <span>/</span> <strong>{{currentPage.title}}</strong></div><div class="top-actions"><span class="tenant-label">当前租户</span><el-select aria-label="当前租户" v-model="tenant" @change="switchTenant" placeholder="选择租户" class="tenant-select"><el-option v-for="t in me.tenants" :key="t.id" :label="t.name" :value="t.id"/></el-select><el-button text @click="passwordDialog=true">账号设置</el-button></div></header>
   <main id="workspace-content" class="content">
    <div class="page-heading"><div><div class="eyebrow">RTTY / WORKSPACE</div><h1>{{currentPage.title}}</h1><p>{{currentPage.description}}</p></div><el-button :loading="loading" @click="refresh">刷新数据</el-button></div>
    <el-alert v-if="error" :title="error" type="error" :closable="false" show-icon class="page-error"/>
    <el-skeleton v-if="loading && !loaded" :rows="6" animated class="panel"/>
    <template v-else>
     <template v-if="tab==='devices'">
      <div class="metrics"><div><span>可见设备</span><strong>{{devices.length}}<small>台</small></strong></div><div><span><i class="status-dot"/>当前在线</span><strong>{{onlineCount}}<small>台</small></strong></div><div><span>可操作设备</span><strong>{{devices.filter(d=>d.can_operate).length}}<small>台</small></strong></div><div><span>所属分组</span><strong>{{groupCount}}<small>组</small></strong></div></div>
      <section class="panel device-panel"><div class="table-heading"><h2>设备列表</h2><span>{{visibleDevices.length}} 台设备</span></div>
           <el-input v-model="search" placeholder="筛选设备编号、名称或分组" clearable class="device-search" aria-label="搜索设备"/>
    <el-table :data="visibleDevices" empty-text="当前租户没有可访问设备">
     <el-table-column label="设备" min-width="220"><template #default="{row}"><div class="device-identity"><span class="device-glyph" aria-hidden="true">▤</span><div><strong>{{row.description||row.wire_id}}</strong><small>{{row.wire_id}}</small></div></div></template></el-table-column><el-table-column label="设备组"><template #default="{row}"><div class="group-tags"><span v-for="g in groupsFor(row)" :key="g">{{g}}</span><span v-if="!groupsFor(row).length">未分组</span></div></template></el-table-column>
     <el-table-column label="状态"><template #default="{row}"><el-tag :type="row.online?'success':'info'">{{row.online?'在线':'离线'}}</el-tag></template></el-table-column>
     <el-table-column label="操作" width="240" align="right"><template #default="{row}"><el-button :disabled="!row.online||!row.can_operate" type="primary" @click="connect(row)">打开终端</el-button><el-button :disabled="!row.online||!row.can_operate" @click="web.dev=row;web.modal=true">设备 Web</el-button></template></el-table-column>
    </el-table><p class="hint">终端保留原有文件传输功能。系统账号权限由设备操作系统管理。</p>

      </section>
     </template>
     <section v-if="tab==='tenants' && manager" class="panel"><el-space><el-input v-model="newTenant" placeholder="租户名称"/><el-button type="primary" @click="createTenant">创建租户</el-button></el-space>
     <el-table empty-text="暂无记录" :data="snapshot.tenants"><el-table-column prop="name" label="名称"/><el-table-column label="操作" min-width="240"><template #default="{row}"><el-button @click="changeDisabled('tenants',row)">{{row.disabled?'启用':'禁用并断开'}}</el-button></template></el-table-column></el-table>
    </section>
<section v-if="tab==='members' && manager" class="panel"><details class="form-disclosure"><summary>添加成员</summary><div class="form-body"><el-space wrap><el-input v-model="account.username" placeholder="新用户名"/><el-input v-model="account.password" type="password" show-password placeholder="密码，12–72字节"/><el-select v-model="account.role" style="width:140px"><el-option label="租户管理员" value="admin"/><el-option label="运维成员" value="operator"/><el-option label="查看者" value="viewer"/></el-select><el-button type="primary" @click="createUser">创建并加入当前租户</el-button></el-space></div></details>
     <el-space v-if="me.user?.platform_admin" wrap class="row"><el-select v-model="member.user_id" placeholder="已有账号" style="width:200px"><el-option v-for="u in snapshot.users" :key="u.id" :label="u.username" :value="u.id"/></el-select><el-select v-model="member.role" style="width:140px"><el-option label="租户管理员" value="admin"/><el-option label="运维成员" value="operator"/><el-option label="查看者" value="viewer"/></el-select><el-button @click="addMember">加入当前租户</el-button></el-space>
     <el-table empty-text="暂无记录" :data="snapshot.members"><el-table-column prop="username" label="成员"/><el-table-column label="角色"><template #default="{row}"><span class="role-tag">{{roleLabel(row.role)}}</span></template></el-table-column><el-table-column label="操作" min-width="240"><template #default="{row}"><el-button :disabled="row.id===me.user?.id" @click="removeMember(row)">移除并撤销会话</el-button></template></el-table-column></el-table>
     <el-table v-if="me.user?.platform_admin" :data="snapshot.users"><el-table-column prop="username" label="平台账号"/><el-table-column label="操作" min-width="240"><template #default="{row}"><el-button :disabled="row.id===me.user?.id" @click="changeDisabled('users',row)">{{row.disabled?'启用':'禁用账号'}}</el-button></template></el-table-column></el-table>
    </section>
<section v-if="tab==='groups' && manager" class="panel"><p class="hint">一台设备可加入当前租户的多个组。权限取所有组授权及设备直接授权的并集。</p>
     <el-space><el-input v-model="newGroup" placeholder="新分组名称" aria-label="新分组名称"/><el-button type="primary" @click="createGroup">创建分组</el-button></el-space>
     <el-table empty-text="暂无记录" :data="snapshot.groups"><el-table-column prop="name" label="分组名称"/><el-table-column label="设备数量"><template #default="{row}">{{(snapshot.device_groups||[]).filter(g=>g.group===row.name).length}}</template></el-table-column><el-table-column label="操作" min-width="240"><template #default="{row}"><el-button @click="renameGroup.old=row.name;renameGroup.name=row.name;renameGroup.open=true">重命名</el-button><el-button type="danger" plain @click="deleteGroup(row)">删除分组</el-button></template></el-table-column></el-table>
    </section>
<section v-if="tab==='inventory' && manager" class="panel"><details class="form-disclosure"><summary>登记新设备</summary><div class="form-body"><el-space wrap><el-input v-model="device.wire_id" placeholder="rtty设备编号（-I）"/><el-input v-model="device.name" placeholder="显示名称"/><el-select v-model="enrollGroups" multiple clearable placeholder="设备分组（可多选）" style="width:260px"><el-option v-for="g in snapshot.groups" :key="g.name" :label="g.name" :value="g.name"/></el-select><el-input v-model="webTargets" placeholder="允许的Web目标，逗号分隔" style="width:320px"/><el-button :disabled="!tenant" @click="enroll">登记并签发凭据</el-button></el-space></div></details>
     <el-alert v-if="credential" title="凭据仅在本次操作显示，请安全保存" type="warning" :closable="false"/><pre v-if="credential" class="credential">{{credential}}</pre><el-button v-if="credential" @click="credential=''">清除显示</el-button>
     <el-table empty-text="暂无记录" :data="snapshot.devices"><el-table-column prop="wire_id" label="设备编号"/><el-table-column label="设备组"><template #default="{row}">{{groupsFor(row).join("、") || "未分组"}}</template></el-table-column><el-table-column label="操作" min-width="240"><template #default="{row}"><el-button @click="editGroups(row)">设置分组</el-button><el-button @click="rotate(row)">轮换并断开</el-button><el-button @click="changeDisabled('devices',row)">{{row.disabled?'启用':'禁用并断开'}}</el-button></template></el-table-column></el-table>
    </section>
<section v-if="tab==='access' && manager" class="panel"><el-space wrap><el-select v-model="grant.user_id" placeholder="成员" style="width:170px"><el-option v-for="u in snapshot.members" :key="u.id" :label="u.username" :value="u.id"/></el-select><el-select v-model="grant.device_id" clearable placeholder="单台设备（可留空）" style="width:220px"><el-option v-for="d in snapshot.devices" :key="d.id" :label="d.wire_id" :value="d.id"/></el-select><el-select v-model="grant.group" :disabled="!!grant.device_id" clearable placeholder="或选择设备组" style="width:220px"><el-option v-for="g in snapshot.groups" :key="g.name" :label="g.name" :value="g.name"/></el-select><el-button @click="changeGrant(true)">授权</el-button><el-button @click="changeGrant(false)">撤销</el-button></el-space>
     <el-table empty-text="暂无记录" :data="grantRows"><el-table-column prop="username" label="成员"/><el-table-column prop="target" label="授权范围"/></el-table><p class="hint">查看者只能查看。运维成员需有设备或设备组授权。租户管理员管理本租户全部设备。</p>
    </section>
<section v-if="tab==='audit' && manager" class="panel"><el-table empty-text="暂无记录" :data="snapshot.audit"><el-table-column label="操作时间" width="190"><template #default="{row}">{{formatTime(row.at)}}</template></el-table-column><el-table-column label="操作" min-width="180"><template #default="{row}">{{actionLabel(row.action)}}</template></el-table-column><el-table-column label="对象" min-width="240" show-overflow-tooltip><template #default="{row}">{{resourceLabel(row.resource)}}</template></el-table-column></el-table></section>
    </template>
    <footer class="workspace-footer"><span>RTTY · 远程运维</span><span>当前空间：{{me.tenants?.find(t=>t.id===tenant)?.name||'未选择租户'}}</span></footer>
   </main>
  </div>
  <RttyWeb v-model="web.modal" :dev="web.dev"/>
    <el-dialog v-model="groupEdit.open" title="设置设备分组" width="460px"><p>{{groupEdit.label}}：可选择多个分组，留空表示未分组。</p><el-checkbox-group v-model="groupEdit.groups" aria-label="设备分组" class="group-options"><el-checkbox v-for="g in snapshot.groups" :key="g.name" :label="g.name" :value="g.name"/></el-checkbox-group><p v-if="!snapshot.groups?.length" class="hint">请先在分组管理中创建分组。</p><p class="hint">移出分组后，仅当没有其他有效授权时才会断开会话。</p><template #footer><el-button @click="groupEdit.open=false">取消</el-button><el-button type="primary" @click="saveGroups">保存分组</el-button></template></el-dialog>
  <el-dialog v-model="renameGroup.open" title="重命名分组" width="400px"><el-input v-model="renameGroup.name" aria-label="分组新名称"/><p class="hint">设备关联和授权保持不变。</p><template #footer><el-button @click="renameGroup.open=false">取消</el-button><el-button type="primary" @click="saveGroupName">保存名称</el-button></template></el-dialog>
  <el-dialog v-model="passwordDialog" title="修改密码" width="400px"><el-form @submit.prevent="changePassword"><el-form-item label="当前密码"><el-input type="password" v-model="password.current_password" autocomplete="current-password"/></el-form-item><el-form-item label="新密码"><el-input type="password" v-model="password.new_password" autocomplete="new-password" placeholder="12–72字节"/></el-form-item><el-button type="primary" @click="changePassword">修改并重新登录</el-button></el-form></el-dialog>

 </div>
</template>
<script setup>
import {ref,reactive,computed,onMounted} from 'vue'
import axios from 'axios'
import {ElMessageBox} from 'element-plus'
import {useRouter} from 'vue-router'
import RttyWeb from '../components/RttyWeb.vue'
const router=useRouter(),me=ref({tenants:[]}),tenant=ref(''),devices=ref([]),snapshot=ref({}),error=ref(''),tab=ref('devices'),search=ref(''),newTenant=ref(''),credential=ref(''),webTargets=ref('')
const account=reactive({username:'',password:'',role:'operator'}),member=reactive({user_id:'',role:'operator'}),device=reactive({wire_id:'',name:'',group:''}),grant=reactive({user_id:'',device_id:'',group:''}),web=reactive({modal:false,dev:null})
const newGroup=ref(''),enrollGroups=ref([]),groupEdit=reactive({open:false,id:'',label:'',groups:[]}),renameGroup=reactive({open:false,old:'',name:''})
const loading=ref(false),loaded=ref(false)
const passwordDialog=ref(false),password=reactive({current_password:'',new_password:''})
const manager=computed(()=>me.value.user?.platform_admin||me.value.user?.role==='admin')
const visibleDevices=computed(()=>devices.value.filter(d=>`${d.wire_id} ${d.description} ${(d.groups||[]).join(" ")}`.toLowerCase().includes(search.value.toLowerCase())))
const grantRows=computed(()=>(snapshot.value.grants||[]).map(g=>({username:snapshot.value.members?.find(u=>u.id===g.user_id)?.username||g.user_id,target:g.device_id?(snapshot.value.devices?.find(d=>d.id===g.device_id)?.wire_id||g.device_id):`设备组：${g.group}`})))
async function refresh(){loading.value=true;try{me.value=(await axios.get('/api/me')).data;tenant.value=me.value.user.tenant_id;devices.value=(await axios.get('/api/devs')).data;snapshot.value=manager.value?(await axios.get('/api/manage/snapshot')).data:{}}catch(e){if(e.response?.status===401)router.push('/login');else error.value='加载失败，请重试'}finally{loading.value=false;loaded.value=true}}
async function action(fn){error.value='';try{await fn();await refresh();return true}catch(e){error.value=e.response?.data?.error||`操作未完成（${e.response?.status||'网络错误'}）`}}
async function switchTenant(){snapshot.value={};devices.value=[];search.value='';credential.value='';enrollGroups.value=[];grant.device_id='';grant.group='';grant.user_id='';groupEdit.open=false;renameGroup.open=false;await action(()=>axios.post('/api/tenant',{tenant_id:tenant.value}))}
async function logout(){await axios.post('/api/signout',{});router.push('/login')}
async function changePassword(){try{await axios.post('/api/password',password);password.current_password='';password.new_password='';passwordDialog.value=false;router.push('/login')}catch(e){error.value=`修改密码失败（${e.response?.status||'网络错误'}）`;passwordDialog.value=false}}
function connect(d){window.open(`/rtty/${encodeURIComponent(d.id)}`,'_blank','noopener')}
async function createTenant(){await action(async()=>{const {data}=await axios.post('/api/manage/tenants',{name:newTenant.value});newTenant.value='';await axios.post('/api/tenant',{tenant_id:data.id})})}
async function createUser(){await action(async()=>{const {data}=await axios.post('/api/manage/users',{username:account.username,password:account.password});account.password='';if(tenant.value)await axios.put('/api/manage/members',{user_id:data.id,tenant_id:tenant.value,role:account.role});account.username=''})}
async function addMember(){await action(()=>axios.put('/api/manage/members',{...member,tenant_id:tenant.value}))}
async function removeMember(u){await action(()=>axios.delete(`/api/manage/members/${u.id}`))}
async function changeDisabled(kind,row){await action(()=>axios.patch(`/api/manage/${kind}/${row.id}`,{disabled:!row.disabled}))}
async function enroll(){await action(async()=>{const {data}=await axios.post('/api/manage/devices',{...device,web_targets:webTargets.value.split(',').map(s=>s.trim()).filter(Boolean)});credential.value=`设备编号：${data.wire_id}\nToken：${data.token}`;if(enrollGroups.value.length)await axios.put(`/api/manage/devices/${data.id}/groups`,{groups:enrollGroups.value})})}
async function rotate(d){await action(async()=>{const {data}=await axios.post(`/api/manage/devices/${d.id}/rotate`,{});credential.value=`设备编号：${d.wire_id}\n新 Token：${data.token}`})}
async function changeGrant(access){await action(()=>axios.put('/api/manage/grants',{...grant,access}))}
function groupsFor(d){return d.groups||(snapshot.value.device_groups||[]).filter(g=>g.device_id===d.id).map(g=>g.group)}
async function createGroup(){await action(async()=>{await axios.post('/api/manage/groups',{name:newGroup.value});newGroup.value=''})}
function editGroups(d){Object.assign(groupEdit,{open:true,id:d.id,label:d.wire_id,groups:[...groupsFor(d)]})}
async function saveGroups(){if(await action(()=>axios.put(`/api/manage/devices/${groupEdit.id}/groups`,{groups:groupEdit.groups})))groupEdit.open=false}
async function saveGroupName(){if(await action(()=>axios.patch(`/api/manage/groups/${encodeURIComponent(renameGroup.old)}`,{name:renameGroup.name})))renameGroup.open=false}
async function deleteGroup(g){try{await ElMessageBox.confirm(`删除“${g.name}”会移除设备关联及成员授权，设备不会删除。没有其他授权的会话将断开。`,'删除分组',{type:'warning',confirmButtonText:'删除分组',cancelButtonText:'取消'})}catch{return}await action(()=>axios.delete(`/api/manage/groups/${encodeURIComponent(g.name)}`))}
onMounted(refresh)

const pages=[
 {key:'devices',icon:'▤',title:'设备总览',description:'查看连接状态，安全访问已授权的设备。'},
 {key:'inventory',icon:'⊞',title:'设备管理',description:'登记设备、配置所属分组，并管理设备接入凭据。'},
 {key:'groups',icon:'▦',title:'分组管理',description:'按项目或团队组织设备，一台设备可以属于多个分组。'},
 {key:'members',icon:'◉',title:'成员管理',description:'管理当前租户的账号、成员角色与访问资格。'},
 {key:'access',icon:'◇',title:'访问授权',description:'为运维成员授予设备或分组权限；有效权限按并集计算。'},
 {key:'audit',icon:'≡',title:'操作记录',description:'查看最近的管理变更与远程访问记录。'},
 {key:'tenants',icon:'⊡',title:'租户管理',description:'管理独立工作空间及其启用状态。'}]
const navigation=computed(()=>pages.filter(p=>p.key==='devices'||(manager.value&&(p.key!=='tenants'||me.value.user?.platform_admin))))
const currentPage=computed(()=>pages.find(p=>p.key===tab.value)||pages[0])
const onlineCount=computed(()=>devices.value.filter(d=>d.online).length)
const groupCount=computed(()=>new Set(devices.value.flatMap(d=>d.groups||[])).size)
function roleLabel(role){return {admin:'租户管理员',operator:'运维成员',viewer:'查看者'}[role]||'未选择租户'}
function formatTime(at){return new Date(Number(at)*1000).toLocaleString('zh-CN',{hour12:false})}
function resourceLabel(id){return snapshot.value.devices?.find(d=>d.id===id)?.wire_id||snapshot.value.members?.find(u=>u.id===id)?.username||id||'—'}
function actionLabel(action){return {'access-open':'开启远程会话','access-close':'关闭远程会话','POST /api/manage/groups':'创建分组','PATCH /api/manage/groups/:name':'重命名分组','DELETE /api/manage/groups/:name':'删除分组','PUT /api/manage/devices/:id/groups':'更新设备分组','PUT /api/manage/grants':'更新访问授权','POST /api/manage/devices':'登记设备','PUT /api/manage/members':'更新成员角色','DELETE /api/manage/members/:id':'移除成员','POST /api/manage/users':'创建账号'}[action]||action}
</script>
<style scoped src="../styles/console.css"></style>
