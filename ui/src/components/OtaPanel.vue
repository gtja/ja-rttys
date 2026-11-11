<template>
 <div class="ota-workspace" v-loading="loading&&!loaded">
  <el-alert v-if="error" :title="error" type="error" show-icon :closable="false"/>
  <el-alert v-if="notice" :title="notice" :type="noticeType" show-icon :closable="false"/>
  <div v-if="loaded&&!data.enabled" class="ota-empty surface"><ConsoleIcon name="device"/><h2>当前租户尚未配置升级后端</h2><p>功能授权已保留，请联系平台管理员完成 OTA 后端与设备绑定配置。</p><el-button @click="load">重新检查</el-button></div>
  <template v-else-if="data.enabled">
   <header class="ota-toolbar">
    <div class="ota-tabs" role="tablist" aria-label="应用升级内容"><button role="tab" :aria-selected="view==='versions'" :class="{active:view==='versions'}" @click="view='versions'">版本库 <span>{{versions.length}}</span></button><button role="tab" :aria-selected="view==='jobs'" :class="{active:view==='jobs'}" @click="view='jobs'">部署记录 <span>{{data.jobs?.length||0}}</span></button></div>
    <div class="ota-actions"><el-button :loading="loading" @click="load"><ConsoleIcon name="refresh"/>刷新</el-button><el-button v-if="data.can_import_artifact" :loading="importing" @click="artifactInput?.click()">导入制品</el-button><input ref="artifactInput" type="file" accept=".mender" hidden @change="importArtifact"><OtaVersionBuilder :enabled="data.can_create_version" @created="created"/><el-button type="primary" :disabled="!data.artifacts?.length||!data.devices?.length" @click="openRelease()">发布升级</el-button></div>
   </header>
   <section v-if="view==='versions'" class="surface ota-library" role="tabpanel" aria-label="版本库">
    <div class="ota-section-head"><div><h2>程序版本</h2><p>可新建版本或导入已有 .mender 制品，再选择设备发布。</p></div><el-input v-model="search" clearable placeholder="搜索版本名称" aria-label="搜索版本名称" class="ota-search"><template #prefix><ConsoleIcon name="search"/></template></el-input></div>
    <el-table :data="filteredVersions" empty-text="暂无匹配版本。可点击“新建版本”制作 PSDK 制品。" row-key="key">
     <el-table-column label="版本" min-width="280"><template #default="{row}"><div class="ota-version-cell"><span class="ota-version-icon"><ConsoleIcon name="folder"/></span><div><b>{{row.label}}</b><small>{{row.types?.join(' · ')||'PSDK 应用制品'}}<template v-if="row.created"> · {{time(row.created)}}</template></small></div></div></template></el-table-column>
     <el-table-column label="状态" width="130"><template #default="{row}"><span :class="['ota-state',tone(row.state)]"><i/>{{versionLabels[row.state]||row.state}}</span></template></el-table-column>
     <el-table-column label="来源" width="150"><template #default="{row}"><span class="ota-muted">{{row.local?'平台制作':'已有制品'}}</span></template></el-table-column>
     <el-table-column label="操作" width="270" align="right"><template #default="{row}"><div class="ota-row-actions"><el-button text @click="showVersion(row)">详情</el-button><el-button plain :disabled="!row.id||!data.devices?.length" @click="openRelease(row.id)">发布此版本</el-button><el-button v-if="row.id" text type="danger" :loading="deleting===row.id" :disabled="!!deleting" @click="deleteArtifact(row)">删除</el-button></div></template></el-table-column>
    </el-table>
    <footer class="ota-table-note"><span>{{filteredVersions.length}} 个版本</span><span>PSDK 镜像需已缓存到目标设备</span></footer>
   </section>
   <section v-else class="surface" role="tabpanel" aria-label="部署记录">
    <div class="ota-section-head"><div><h2>部署记录</h2><p>提交状态表示任务已交给升级服务，设备执行结果请查看详情。</p></div></div>
    <el-table :data="data.jobs||[]" empty-text="暂无部署任务。选择一个版本开始发布。" row-key="id">
     <el-table-column label="版本 / 任务" min-width="280"><template #default="{row}"><div class="ota-job-cell"><b>{{displayName(row.artifact)}}</b><small>{{row.id}}</small></div></template></el-table-column>
     <el-table-column label="目标设备" min-width="160"><template #default="{row}">{{targetNames(row.targets)}}</template></el-table-column>
     <el-table-column label="提交状态" width="145"><template #default="{row}"><span :class="['ota-state',row.state==='submitted'?'neutral':'warning']"><i/>{{submitLabels[row.state]||row.state}}</span></template></el-table-column>
     <el-table-column label="提交时间" width="180"><template #default="{row}"><span class="ota-muted">{{time(row.created_at)}}</span></template></el-table-column>
     <el-table-column width="100" align="right"><template #default="{row}"><el-button text @click="showJob(row)">查看结果</el-button></template></el-table-column>
    </el-table><footer class="ota-table-note">显示最近 30 条任务</footer>
   </section>
   <div class="ota-context"><ConsoleIcon name="key"/><span>{{data.devices?.length||0}} 台设备已绑定升级服务。版本与任务仅在当前租户内可见。</span></div>
  </template>

  <el-dialog v-model="importOpen" title="导入 Mender 制品" width="min(460px,94vw)" :show-close="!importing" :close-on-click-modal="!importing" :close-on-press-escape="!importing">
   <p>已选择 {{importFile?.name}}</p>
   <p class="ota-muted">制品内部名称需以 <code>{{data.artifact_prefix}}</code> 开头，文件上限 128 MiB。</p>
   <template #footer><el-button :disabled="importing" @click="importOpen=false">取消</el-button><el-button type="primary" :loading="importing" :disabled="!importFile" @click="submitImport">确认导入</el-button></template>
  </el-dialog>
  <el-drawer v-model="releaseOpen" title="发布程序升级" size="min(580px,100vw)" class="console-drawer" :close-on-click-modal="!publishing" :show-close="!publishing" :close-on-press-escape="!publishing">
   <p class="ota-drawer-intro">确认版本和目标设备。升级、降级都使用同一发布流程。</p>
   <el-alert v-if="releaseError" :title="releaseError" type="error" :closable="false"/>
   <el-alert v-if="selectionNotice" :title="selectionNotice" :type="selectionMatched?'info':'warning'" :closable="false"/>
   <el-form label-position="top" class="ota-release-form">
    <el-form-item label="01 · 选择程序版本"><el-select v-model="artifact" filterable :disabled="publishing" placeholder="请选择可发布版本" style="width:100%" aria-label="选择程序版本"><el-option v-for="a in data.artifacts||[]" :key="a.id" :value="a.id" :label="displayName(a.name)"/></el-select></el-form-item>
    <div v-if="selectedArtifact" class="ota-selected-version"><span>兼容设备类型</span><b>{{selectedArtifact.device_types_compatible?.join('、')||'以制品声明为准'}}</b></div>
    <el-form-item label="02 · 选择目标设备"><el-checkbox-group v-model="targets" class="ota-targets" :disabled="publishing"><el-checkbox v-for="d in data.devices||[]" :key="d.id" :value="d.id"><b>{{d.name||d.wire_id}}</b><small>{{d.wire_id}}</small></el-checkbox></el-checkbox-group><p v-if="!data.devices?.length" class="ota-muted">暂无已绑定 OTA 的设备，请联系管理员。</p></el-form-item>
   </el-form>
   <div class="ota-release-note"><ConsoleIcon name="device"/><p>发布前请确认设备处于维护窗口。PSDK 镜像需已在盒子上缓存；离线设备领取任务的时间取决于恢复连接的时间。</p></div>
   <template #footer><div class="ota-release-footer"><span>已选 {{targets.length}} 台设备</span><el-button :disabled="publishing" @click="releaseOpen=false">取消</el-button><el-button type="primary" :loading="publishing" :disabled="!selectedArtifact||!targets.length" @click="publish">确认并发布</el-button></div></template>
  </el-drawer>

  <el-drawer v-model="versionOpen" @closed="versionDetail=null" title="版本详情" size="min(560px,100vw)" class="console-drawer">
   <template v-if="versionDetail"><span :class="['ota-state',tone(versionDetail.state)]"><i/>{{versionLabels[versionDetail.state]||versionDetail.state}}</span><h2 class="ota-detail-title">{{versionDetail.label}}</h2><dl class="ota-metadata"><dt>完整制品名称</dt><dd>{{versionDetail.name}}</dd><dt>兼容设备</dt><dd>{{versionDetail.types?.join('、')||'ja-nvidia-arm64-app'}}</dd><template v-if="versionDetail.local"><dt>镜像 ID</dt><dd>{{versionDetail.local.image}}</dd><dt>程序 SHA-256</dt><dd>{{versionDetail.local.binary_sha256}}</dd><dt>模型 SHA-256</dt><dd>{{versionDetail.local.model_sha256}}</dd></template></dl><p v-if="['unknown','interrupted'].includes(versionDetail.state)" class="ota-release-note">导入结果未完全确认，请联系管理员核对，避免重复创建。</p></template>
   <template #footer><el-button type="primary" :disabled="!versionDetail?.id||!data.devices?.length" @click="releaseFromDetail">发布此版本</el-button></template>
  </el-drawer>

  <el-drawer v-model="jobOpen" title="部署结果" size="min(600px,100vw)" class="console-drawer">
   <template v-if="activeJob"><div class="ota-result-heading"><span :class="['ota-state',jobTone]"><i/>{{executionLabel}}</span><el-button :loading="jobLoading" @click="fetchJob">刷新结果</el-button></div><h2 class="ota-detail-title">{{displayName(activeJob.artifact)}}</h2><p class="ota-muted">{{targetNames(activeJob.targets)}}</p><el-alert v-if="jobError" :title="jobError" type="error" :closable="false"/>
    <div v-if="jobData?.stats" class="ota-result-counts"><div><strong>{{jobData.stats.success||0}}</strong><span>成功</span></div><div><strong>{{jobData.stats.failure||0}}</strong><span>失败</span></div><div><strong>{{otherResults}}</strong><span>其他 / 待执行</span></div></div>
    <p class="ota-result-help">{{executionHelp}}</p><dl class="ota-metadata"><dt>任务编号</dt><dd>{{activeJob.id}}</dd><dt>完整制品名称</dt><dd>{{activeJob.artifact}}</dd><dt>提交时间</dt><dd>{{time(activeJob.created_at)}}</dd></dl>
    <div v-if="jobData?.stats" class="ota-stat-list"><div v-for="(value,key) in nonzeroStats" :key="key"><span>{{statLabels[key]||key}}</span><b>{{value}}</b></div></div>
   </template>
  </el-drawer>
 </div>
</template>
<script setup>
import {ref,computed,onMounted,onBeforeUnmount,watch} from 'vue'
import axios from 'axios'
import {ElMessageBox} from 'element-plus'
import OtaVersionBuilder from './OtaVersionBuilder.vue'
import ConsoleIcon from './ConsoleIcon.vue'
const props=defineProps({initialDevice:Object})
const data=ref({}),loading=ref(false),loaded=ref(false),error=ref(''),notice=ref(''),noticeType=ref('success'),view=ref('versions'),search=ref('')
const releaseOpen=ref(false),publishing=ref(false),releaseError=ref(''),artifact=ref(''),targets=ref([]),versionDetail=ref(null),versionOpen=ref(false)
const artifactInput=ref(null),importOpen=ref(false),importFile=ref(null),importing=ref(false),deleting=ref('')
const jobOpen=ref(false),activeJob=ref(null),jobData=ref(null),jobLoading=ref(false),jobError=ref('')
let initialized=false,pending=null,timer=null,disposed=false,jobSequence=0
const versionLabels={ready:'可发布',building:'制作中',failed:'制作失败',unknown:'结果待确认',rejected:'导入被拒绝',interrupted:'制作中断',unavailable:'制品不可用'}
const submitLabels={creating:'正在提交',submitted:'已提交',unknown:'提交待确认',rejected:'提交被拒绝'}
const statLabels={success:'成功',failure:'失败',pending:'等待设备',downloading:'下载中',installing:'安装中',rebooting:'重启中',aborted:'已取消','already-installed':'已安装该版本',noartifact:'无兼容制品',decommissioned:'设备已注销',pause_before_installing:'等待安装',pause_before_committing:'等待提交',pause_before_rebooting:'等待重启'}
const tone=s=>s==='ready'?'success':['failed','rejected'].includes(s)?'danger':'warning'
const time=v=>v?new Date(v*1000).toLocaleString():'—'
const displayName=name=>(name||'').replace(/^rttys-[a-f0-9]{12,64}-/,'')
const versions=computed(()=>{
 const artifacts=data.value.artifacts||[],local=data.value.versions||[]
 const rows=artifacts.map(a=>{const v=local.find(v=>v.artifact===a.name);return {key:a.id,id:a.id,name:a.name,label:v?.version||displayName(a.name),state:'ready',types:a.device_types_compatible,created:v?.created_at,local:v}})
 for(const v of local)if(!artifacts.some(a=>a.name===v.artifact))rows.push({key:'local-'+v.version,id:'',name:v.artifact,label:v.version,state:v.state==='ready'?'unavailable':v.state,local:v,created:v.created_at,types:['ja-nvidia-arm64-app']})
 return rows.sort((a,b)=>(b.created||0)-(a.created||0)||a.label.localeCompare(b.label))
})
const filteredVersions=computed(()=>versions.value.filter(v=>`${v.label} ${v.name}`.toLowerCase().includes(search.value.toLowerCase())))
const selectedArtifact=computed(()=>data.value.artifacts?.find(a=>a.id===artifact.value))
const selectionMatched=computed(()=>data.value.devices?.some(d=>d.id===props.initialDevice?.id))
const selectionNotice=computed(()=>!props.initialDevice?'':selectionMatched.value?`已带入 ${props.initialDevice.name||props.initialDevice.wire_id}，请确认目标。`:`${props.initialDevice.name||props.initialDevice.wire_id} 尚未配置可用的 OTA 绑定；未自动选择其他设备。`)
function targetIDs(value){try{const parsed=Array.isArray(value)?value:JSON.parse(value||'[]');return Array.isArray(parsed)?parsed:[]}catch{return []}}
function targetNames(value){return targetIDs(value).map(id=>data.value.devices?.find(d=>d.id===id)?.name||data.value.devices?.find(d=>d.id===id)?.wire_id||id).join('、')||'—'}
async function load(){if(loading.value)return;loading.value=true;error.value='';try{const result=(await axios.get('/api/ota')).data;if(disposed)return;data.value=result;const allowed=new Set((result.devices||[]).map(d=>d.id));targets.value=targets.value.filter(id=>allowed.has(id));if(!result.artifacts?.some(a=>a.id===artifact.value))artifact.value='';if(!initialized){initialized=true;if(props.initialDevice){targets.value=allowed.has(props.initialDevice.id)?[props.initialDevice.id]:[];releaseOpen.value=true}}loaded.value=true}catch(e){if(!disposed)error.value=e.response?.data?.error||'无法加载应用升级，请重试'}finally{loading.value=false}}
function openRelease(id=''){artifact.value=id;releaseError.value='';releaseOpen.value=true}
function showVersion(row){versionDetail.value=row;versionOpen.value=true}
function releaseFromDetail(){const id=versionDetail.value?.id;versionOpen.value=false;openRelease(id)}
async function created(v){await load();artifact.value=v.artifact_id;view.value='versions';noticeType.value='success';notice.value=`版本 ${v.version} 已就绪，可在版本库中发布。`}
function importArtifact(event){
 const file=event.target.files?.[0]
 event.target.value=''
 if(!file)return
 if(!/\.mender$/i.test(file.name)||!file.size||file.size>128*1024*1024){
  error.value='请选择非空且不超过 128 MiB 的 .mender 文件'
  return
 }
 importFile.value=file
 importOpen.value=true
}
async function submitImport(){
 if(importing.value||!importFile.value)return
 importing.value=true
 notice.value=''
 error.value=''
 const form=new FormData()
 form.append('artifact',importFile.value)
 try{
  const {data:r}=await axios.post('/api/ota/artifacts',form)
  importOpen.value=false
  importFile.value=null
  view.value='versions'
  await load()
  noticeType.value='success'
  notice.value=`制品 ${r.artifact} 已导入。`
 }catch(e){
  importOpen.value=false
  error.value=e.response?.data?.error||'导入结果未确认，请刷新版本库后检查'
 }finally{importing.value=false}
}
async function deleteArtifact(row){
 if(deleting.value||!row.id)return
 try{
  await ElMessageBox.confirm(`确认从 Mender 删除制品「${row.name}」？删除后此制品将无法再发布。`,'删除制品',{confirmButtonText:'删除制品',cancelButtonText:'取消',type:'warning',confirmButtonClass:'el-button--danger'})
 }catch{return}
 deleting.value=row.id
 notice.value=''
 error.value=''
 try{
  await axios.delete(`/api/ota/artifacts/${encodeURIComponent(row.id)}`,{data:{name:row.name}})
  await load()
  noticeType.value='success'
  notice.value=`制品 ${row.name} 已删除。`
 }catch(e){
  error.value=e.response?.data?.error||'无法删除制品，请刷新后重试'
 }finally{deleting.value=''}
}
async function publish(){
 if(publishing.value||!selectedArtifact.value||!targets.value.length)return
 const chosen=selectedArtifact.value,ids=[...targets.value],names=targetNames(ids)
 try{await ElMessageBox.confirm(`版本：${displayName(chosen.name)}\n目标：${names}\n共 ${ids.length} 台设备。确认在维护窗口内发布？`,'确认发布升级',{confirmButtonText:'发布到所选设备',cancelButtonText:'返回检查',type:'warning'})}catch{return}
 const signature=JSON.stringify([chosen.id,[...ids].sort()]);if(!pending||pending.signature!==signature)pending={signature,id:crypto.randomUUID()}
 publishing.value=true;releaseError.value=''
 try{const {data:r}=await axios.post('/api/ota/deployments',{request_id:pending.id,artifact_id:chosen.id,device_ids:ids});pending=null;releaseOpen.value=false;noticeType.value=r.state==='submitted'?'success':'warning';notice.value=r.state==='submitted'?'任务已提交。请在部署记录中查看设备执行结果。':`任务${submitLabels[r.state]||r.state}，请先核对结果，避免重复发布。`;view.value='jobs';await load();const row=data.value.jobs?.find(j=>j.id===r.id);if(row)showJob(row)}catch(e){releaseError.value=e.response?.data?.error||'提交结果未确认，请先刷新部署记录；重试会复用同一任务编号。'}finally{publishing.value=false}
}
const nonzeroStats=computed(()=>Object.fromEntries(Object.entries(jobData.value?.stats||{}).filter(([,v])=>v>0)))
const otherResults=computed(()=>Object.entries(jobData.value?.stats||{}).filter(([k])=>!['success','failure'].includes(k)).reduce((sum,[,v])=>sum+v,0))
const executionLabel=computed(()=>!jobData.value?(jobError.value?'结果暂不可用':'正在读取结果'):jobData.value.status==='finished'?(jobData.value.stats?.failure?'已结束 · 存在失败':jobData.value.stats?.success&&!otherResults.value?'已结束 · 全部成功':'已结束 · 查看明细'):jobData.value.status==='inprogress'?'设备执行中':jobData.value.status==='pending'?'等待设备领取':submitLabels[jobData.value.job?.state]||jobData.value.status||'结果待确认')
const jobTone=computed(()=>jobData.value?.stats?.failure?'danger':jobData.value?.status==='finished'&&jobData.value?.stats?.success&&!otherResults.value?'success':'neutral')
const executionHelp=computed(()=>jobData.value?.stats?.failure?'部署包含失败设备。失败不代表回滚已验证，请结合设备状态确认恢复结果。':jobData.value?.status==='finished'?'结果来自升级服务；业务功能仍需按现场验收要求确认。':'详情保持打开时会自动刷新。设备离线或等待领取时，任务尚未完成。')
async function showJob(row){activeJob.value=row;jobData.value=null;jobOpen.value=true;await fetchJob()}
async function fetchJob(){const row=activeJob.value;if(!row)return;const seq=++jobSequence;clearTimeout(timer);jobLoading.value=true;jobError.value='';try{const {data:r}=await axios.get(`/api/ota/deployments/${encodeURIComponent(row.id)}`);if(disposed||seq!==jobSequence||!jobOpen.value)return;jobData.value=r;if(r.status&&r.status!=='finished')timer=setTimeout(fetchJob,5000)}catch(e){if(seq===jobSequence&&!disposed)jobError.value=e.response?.data?.error||'暂时无法读取结果，请刷新重试'}finally{if(seq===jobSequence)jobLoading.value=false}}
watch(jobOpen,value=>{if(!value){clearTimeout(timer);jobSequence++;jobLoading.value=false}})
onMounted(load);onBeforeUnmount(()=>{disposed=true;jobSequence++;clearTimeout(timer)})
</script>
<style scoped>
.ota-workspace{max-width:1440px;margin:0 auto;color:#303c50}.ota-workspace>.el-alert{margin-bottom:16px}.ota-toolbar{display:flex;align-items:center;justify-content:space-between;gap:20px;margin-bottom:20px}.ota-tabs{display:flex;gap:24px;align-self:stretch;border-bottom:1px solid #e1e5ec}.ota-tabs button{font:inherit;font-size:14px;font-weight:550;color:#7a8495;background:none;border:0;border-bottom:2px solid transparent;padding:14px 0;cursor:pointer;white-space:nowrap}.ota-tabs button.active{color:#344ba9;border-bottom-color:#455cd0}.ota-tabs span{font-size:11px;margin-left:6px;color:#8a94a6;font-variant-numeric:tabular-nums}.ota-actions{display:flex;align-items:center;gap:8px}.ota-actions :deep(.el-button+.el-button){margin-left:0}.ota-actions svg{width:14px;height:14px;margin-right:5px}.ota-section-head{padding:26px 26px 22px;display:flex;align-items:center;justify-content:space-between;gap:24px}.ota-section-head h2{font-size:17px;letter-spacing:-.3px;margin:0 0 8px;font-weight:600}.ota-section-head p{font-size:12px;color:#758197;line-height:1.7;margin:0}.ota-search{max-width:240px;flex-shrink:0}.ota-version-cell{display:flex;align-items:center;gap:12px;min-height:44px}.ota-version-icon{width:34px;height:36px;display:grid;place-items:center;background:#f1f3f9;border-radius:6px;color:#7a88a8;flex-shrink:0}.ota-version-cell b,.ota-job-cell b{font-size:13px;font-weight:550;color:#3d4a61;overflow-wrap:anywhere;display:block;line-height:1.7}.ota-version-cell small,.ota-job-cell small{display:block;margin-top:4px;color:#7a8598;font-size:11px;line-height:1.7;overflow-wrap:anywhere}.ota-job-cell small{font-family:ui-monospace,monospace;font-size:10px}.ota-row-actions{display:inline-flex;align-items:center;gap:8px;white-space:nowrap}.ota-row-actions :deep(.el-button+.el-button){margin-left:0}.ota-state{display:inline-flex;align-items:center;gap:7px;font-size:12px;line-height:1.5;color:#647089}.ota-state i{width:6px;height:6px;background:currentColor;border-radius:50%;flex-shrink:0}.ota-state.success{color:#2c8768}.ota-state.warning{color:#a97525}.ota-state.danger{color:#bd5252}.ota-muted{color:#7b879a;font-size:12px;line-height:1.8}.ota-table-note{padding:17px 26px;display:flex;justify-content:space-between;gap:16px;font-size:11px;color:#8791a2;border-top:1px solid #edf0f4}.ota-context{display:flex;align-items:center;gap:8px;color:#7e899c;font-size:11px;margin:18px 4px;line-height:1.7}.ota-context svg{width:14px;height:14px}.ota-empty{padding:64px 24px;text-align:center}.ota-empty>svg{width:32px;height:32px;color:#96a1b8}.ota-empty h2{font-size:18px;font-weight:550}.ota-empty p{color:#7d889a;margin-bottom:24px}.ota-drawer-intro{color:#78849a;font-size:13px;line-height:1.8;margin:0 0 24px}.ota-release-form{margin-top:26px}.ota-release-form :deep(.el-form-item){margin-bottom:28px}.ota-release-form :deep(.el-form-item__label){font-size:13px;font-weight:550;color:#45536c}.ota-selected-version{display:flex;flex-direction:column;gap:7px;padding:14px 16px;background:#f6f7fb;border-radius:6px;margin:-12px 0 30px;font-size:12px}.ota-selected-version span{color:#929bae;font-size:11px}.ota-selected-version b{font-weight:500;color:#5b6880;overflow-wrap:anywhere}.ota-targets{display:flex;flex-direction:column;gap:10px;width:100%}.ota-targets :deep(.el-checkbox){margin:0;height:auto;min-height:64px;border:1px solid #e4e8ef;border-radius:7px;padding:14px;align-items:center}.ota-targets :deep(.el-checkbox.is-checked){background:#f5f7ff;border-color:#bdc8ec}.ota-targets :deep(.el-checkbox__label){white-space:normal;overflow-wrap:anywhere;line-height:1.5}.ota-targets b{display:block;font-size:13px;font-weight:550}.ota-targets small{display:block;font-size:11px;color:#8993a6;margin-top:5px}.ota-release-note{display:flex;gap:10px;padding:16px;background:#f7f8fb;border-radius:6px;color:#79859a;font-size:12px;line-height:1.8;margin-top:24px}.ota-release-note svg{width:16px;height:16px;flex-shrink:0;margin-top:3px}.ota-release-note p{margin:0}.ota-release-footer{display:flex;align-items:center;justify-content:flex-end;gap:8px}.ota-release-footer>span{margin-right:auto;color:#8490a4;font-size:12px}.ota-release-footer :deep(.el-button+.el-button){margin:0}.ota-detail-title{font-size:21px;line-height:1.5;letter-spacing:-.5px;overflow-wrap:anywhere;margin:20px 0 12px;color:#34425b}.ota-metadata{margin-top:28px}.ota-metadata dt{color:#8792a5;font-size:12px;margin:20px 0 8px}.ota-metadata dd{margin:0;color:#53617a;font-size:12px;font-family:ui-monospace,monospace;line-height:1.8;overflow-wrap:anywhere}.ota-result-heading{display:flex;align-items:center;justify-content:space-between;gap:16px}.ota-result-counts{display:grid;grid-template-columns:repeat(3,1fr);border-top:1px solid #e8ebf1;border-bottom:1px solid #e8ebf1;padding:24px 0;margin:26px 0 18px}.ota-result-counts>div{display:flex;flex-direction:column;gap:8px;padding-left:18px;border-left:1px solid #e8ebf1}.ota-result-counts>div:first-child{padding-left:0;border:0}.ota-result-counts strong{font-size:27px;font-weight:550;color:#40506a;font-variant-numeric:tabular-nums}.ota-result-counts span{font-size:12px;color:#8792a5}.ota-result-help{font-size:12px;color:#7d889b;line-height:1.9}.ota-stat-list{margin-top:24px;border-top:1px solid #edf0f4}.ota-stat-list>div{padding:12px 0;display:flex;justify-content:space-between;font-size:12px;color:#758197;border-bottom:1px solid #edf0f4}.ota-stat-list b{font-weight:550;color:#40506a}.ota-workspace button:focus-visible{outline:2px solid #7185d2;outline-offset:4px}
@media(max-width:1000px){.ota-toolbar{flex-wrap:wrap}.ota-section-head{align-items:flex-start;flex-direction:column}.ota-search{max-width:none;width:100%}.ota-actions{flex-wrap:wrap}}@media(max-width:600px){.ota-toolbar{gap:12px}.ota-actions{width:100%}.ota-tabs{gap:22px}.ota-section-head{padding:20px}.ota-table-note{padding:15px 20px;flex-direction:column;gap:6px}.ota-release-footer{flex-wrap:wrap}.ota-release-footer>span{width:100%}.ota-result-counts strong{font-size:23px}}
</style>
