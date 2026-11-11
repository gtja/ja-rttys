<template>
 <el-drawer :model-value="true" @close="$emit('close')" :title="data.device?.name||device.wire_id" size="min(1000px,100vw)">
  <el-alert v-if="error" :title="error" type="error" :closable="false"/>
  <div style="margin:12px 0"><span>操作链路：</span><el-select v-model="link" clearable placeholder="自动优选（5G → 1.4G）" @change="load"><el-option v-for="l in data.links||[]" :key="l.link" :value="l.link" :label="l.link==='14g'?'1.4G':l.link==='5g'?'5G':l.link"/></el-select></div>
  <el-tabs v-model="tab">
   <el-tab-pane label="设备详情" name="detail">
    <el-descriptions :column="2" border v-if="data.device">
     <el-descriptions-item label="平台登记编号">{{data.device.wire_id}}</el-descriptions-item><el-descriptions-item label="状态">{{data.connection.online?'在线':'离线'}}</el-descriptions-item>
     <el-descriptions-item label="当前连接 -I">{{data.connection.client_id||'离线，连接后显示'}}</el-descriptions-item><el-descriptions-item label="注册描述 -D">{{data.device.client_description||'—'}}</el-descriptions-item><el-descriptions-item label="分组">{{data.device.groups?.join('、')||'未分组'}}</el-descriptions-item>
     <el-descriptions-item label="最后注册">{{time(data.device.last_registered_at)}}</el-descriptions-item><el-descriptions-item label="本次连接">{{time(data.connection.connected_at)}}</el-descriptions-item>
     <el-descriptions-item label="已连接时长">{{data.connection.online?duration(data.connection.connected):'—'}}</el-descriptions-item><el-descriptions-item label="运行时长">{{!data.connection.online?'—':data.connection.uptime?duration(data.connection.uptime):'等待上报'}}</el-descriptions-item>
     <el-descriptions-item label="来源地址">{{data.connection.source||'—'}}</el-descriptions-item><el-descriptions-item label="客户端协议">{{data.connection.protocol||'未知'}}</el-descriptions-item>
    </el-descriptions>
    <h3>PSDK 运行版本</h3>
    <el-descriptions :column="1" border>
     <el-descriptions-item label="容器镜像">{{data.runtime?.info?.image||'尚未采集'}}</el-descriptions-item>
     <el-descriptions-item label="镜像 ID">{{data.runtime?.info?.image_id||'—'}}</el-descriptions-item>
     <el-descriptions-item label="镜像声明版本">{{data.runtime?.info?.program_version||'镜像未声明'}}</el-descriptions-item>
     <el-descriptions-item label="最近成功采集">{{time(data.runtime?.collected_at)}}</el-descriptions-item>
     <el-descriptions-item label="本次采集">{{runtimeStatus(data.runtime)}} · {{time(data.runtime?.attempted_at)}}</el-descriptions-item>
    </el-descriptions>
    <el-alert v-if="data.runtime?.error" :title="data.runtime.error" type="warning" :closable="false"/>
    <p class="secondary">每次设备上线后自动采集。失败时保留上次成功结果及时间。</p>
    <p class="secondary">离线原因需要设备日志确认。当前客户端只上报协议号，未上报软件版本号。</p>
    <el-button @click="load">刷新状态</el-button>
    <h3>最近操作</h3><el-table :data="data.events||[]"><el-table-column label="时间"><template #default="{row}">{{time(row.at)}}</template></el-table-column><el-table-column prop="action" label="操作"/><el-table-column prop="result" label="结果"/></el-table>
   </el-tab-pane>
   <el-tab-pane v-if="filesEnabled" label="文件管理" name="files">
    <template v-if="data.operations?.username"><p>执行账号：{{data.operations.username}}</p><DeviceFiles v-if="tab==='files'&&device.can_operate&&data.connection?.online" :key="`${profileKey}-${link}`" :device-id="device.id" :link="link" :root="data.operations.root"/><el-empty v-else description="设备需在线且账号具有操作权限"/></template>
    <el-empty v-else description="请由平台管理员配置执行账号和允许访问的目录"/>
   </el-tab-pane>
   <el-tab-pane v-if="platformAdmin" label="运维设置" name="settings">
    <p>文件管理和巡检以此非 root 账号执行，只访问指定目录。设备需要 Python 3。变更配置会撤销正在进行的远程会话。</p>
    <el-form label-position="top"><el-form-item label="系统账号"><el-input v-model="profile.username" placeholder="nvidia"/></el-form-item><el-form-item label="文件目录"><el-input v-model="profile.root" placeholder="/"/></el-form-item><el-button type="primary" @click="save">保存运维设置</el-button></el-form>
   </el-tab-pane>
  </el-tabs>
 </el-drawer>
</template>
<script setup>
import {ref,reactive,onMounted} from 'vue'
import axios from 'axios'
import DeviceFiles from './DeviceFiles.vue'
const props=defineProps({device:Object,platformAdmin:Boolean,filesEnabled:{type:Boolean,default:true}});defineEmits(['close'])
const link=ref(props.device.preferred_link||'')
const data=ref({}),tab=ref('detail'),error=ref(''),profile=reactive({username:'nvidia',root:'/'}),profileKey=ref(0)
function duration(value){const n=Math.max(0,Math.floor(Number(value)||0));const days=Math.floor(n/86400);return `${days?days+' 天 ':''}${[Math.floor(n%86400/3600),Math.floor(n%3600/60),n%60].map(x=>String(x).padStart(2,'0')).join(':')}`}
const runtimeStatus=r=>({pending:'等待上线采集',collecting:'采集中',ready:'采集成功',failed:'采集失败'}[r?.status]||'等待上线采集')
const time=v=>v?new Date(v*1000).toLocaleString():'尚无记录'
async function load(){error.value='';try{data.value=(await axios.get(`/api/devices/${props.device.id}/overview`,{params:{link:link.value}})).data;if(data.value.operations.username)Object.assign(profile,data.value.operations)}catch(e){error.value=e.response?.data?.error||'无法读取设备详情'}}
async function save(){try{await axios.put(`/api/manage/devices/${props.device.id}/operations`,profile);profileKey.value++;await load();tab.value='files'}catch(e){error.value=e.response?.data?.error||'保存失败'}}
onMounted(load)
</script>
