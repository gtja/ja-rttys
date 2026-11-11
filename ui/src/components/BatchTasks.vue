<template>
 <section class="surface task-panel">
  <el-alert v-if="error" :title="error" type="error" :closable="false"/>
  <p>按设备选择目标；分组用于筛选，重复设备只执行一次。当前提供只读巡检，权限在每台设备执行前重新检查。</p>
  <div class="task-controls"><el-select v-model="group" clearable placeholder="全部分组"><el-option v-for="g in groups" :key="g" :label="g" :value="g"/></el-select><el-select v-model="template"><el-option label="磁盘检查" value="disk"/><el-option label="服务状态（失败服务）" value="services"/><el-option label="日志采集（当前系统账号可读）" value="logs"/></el-select><el-button type="primary" :loading="busy" :disabled="!selected.length" @click="submit">执行巡检（{{selected.length}}）</el-button></div>
  <el-table :data="filtered" row-key="id" @selection-change="rows=>selected=rows.map(x=>x.id)"><el-table-column type="selection" :selectable="d=>d.online&&d.can_operate"/><el-table-column prop="wire_id" label="设备"/><el-table-column label="状态"><template #default="{row}">{{row.online?'在线':'离线'}}</template></el-table-column></el-table>
  <div class="task-controls"><h3>任务记录</h3><el-button @click="load">刷新</el-button></div>
  <el-table :data="jobs" empty-text="暂无任务"><el-table-column type="expand"><template #default="{row}"><div v-for="r in row.results" :key="r.device_id" class="task-result"><b>{{label(r.device_id)}} · {{r.status==='success'?'成功':'失败'}}</b><pre>{{r.error||JSON.stringify(r.output,null,2)}}</pre></div></template></el-table-column><el-table-column label="创建时间"><template #default="{row}">{{new Date(row.created_at*1000).toLocaleString()}}</template></el-table-column><el-table-column prop="template" label="模板"/><el-table-column label="进度"><template #default="{row}">{{row.results.length}} / {{row.targets.length}}</template></el-table-column><el-table-column label="状态"><template #default="{row}">{{({running:'执行中',finished:'已结束',interrupted:'服务重启中断，未自动重试'})[row.state]||row.state}}</template></el-table-column></el-table>
 </section>
</template>
<script setup>
import {ref,computed,onMounted,onBeforeUnmount} from 'vue'
import {ElMessageBox} from 'element-plus'
import axios from 'axios'
const props=defineProps({devices:Array});const group=ref(''),template=ref('disk'),selected=ref([]),jobs=ref([]),error=ref(''),busy=ref(false)
const groups=computed(()=>[...new Set(props.devices.flatMap(x=>x.groups||[]))]);const filtered=computed(()=>props.devices.filter(x=>!group.value||(x.groups||[]).includes(group.value)))
const label=id=>props.devices.find(x=>x.id===id)?.wire_id||id
let timer,disposed=false,pending=null
async function load(){try{const r=(await axios.get('/api/tasks')).data;if(!disposed)jobs.value=r}catch(e){if(!disposed)error.value=e.response?.data?.error||'读取任务失败'}}
async function submit(){
 const ids=[...new Set(selected.value)];if(ids.length>20){error.value='单次最多选择 20 台设备';return}
 try{await ElMessageBox.confirm(`对 ${ids.length} 台设备执行只读巡检？`,'确认任务',{confirmButtonText:'开始执行',cancelButtonText:'取消'})}catch{return}
 const signature=JSON.stringify([template.value,ids]);if(!pending||pending.signature!==signature)pending={signature,id:crypto.randomUUID()}
 busy.value=true;error.value='';try{await axios.post('/api/tasks',{id:pending.id,template:template.value,device_ids:ids});pending=null;await load()}catch(e){error.value=e.response?.data?.error||'提交未确认，请刷新任务列表后重试'}finally{busy.value=false}
}
onMounted(()=>{load();timer=setInterval(()=>{if(jobs.value.some(x=>x.state==='running'))load()},3000)});onBeforeUnmount(()=>{disposed=true;clearInterval(timer)})
</script>
<style scoped>.task-panel{padding:24px}.task-controls{display:flex;gap:12px;align-items:center;margin:20px 0;flex-wrap:wrap}.task-controls .el-select{width:220px}.task-controls h3{flex:1}.task-result{padding:12px 24px}.task-result pre{white-space:pre-wrap;overflow-wrap:anywhere;background:#f5f5f5;padding:12px}</style>
