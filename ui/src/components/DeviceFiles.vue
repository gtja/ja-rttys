<template>
 <section>
  <el-alert v-if="error" :title="error" type="error" :closable="false"/>
  <div class="file-controls"><el-button :disabled="busy||path==='.'" @click="parent">上一级</el-button><code>{{absolutePath}}</code><el-button :disabled="busy" @click="list">刷新</el-button><label class="file-upload">上传文件<input type="file" :disabled="busy" @change="choose"/></label></div>
  <p class="secondary">目录范围：{{root}}。单文件上限 16 MiB，不覆盖同名文件；大文件可继续使用终端传输。</p>
  <div v-if="busy"><el-progress :percentage="progress"/><span>{{phase}}</span><el-button text @click="cancelTransfer">取消传输</el-button></div>
  <el-button v-if="retryFile&&!busy" @click="upload(retryFile)">重新上传 {{retryFile.name}}</el-button>
  <el-table :data="entries" empty-text="目录为空或尚未加载">
   <el-table-column label="名称" min-width="200"><template #default="{row}"><el-button v-if="row.kind==='directory'" link :disabled="busy" @click="enter(row.name)">{{row.name}} /</el-button><span v-else>{{row.name}}</span></template></el-table-column>
   <el-table-column prop="kind" label="类型" width="110"/>
   <el-table-column label="大小" width="110"><template #default="{row}">{{row.kind==='file'?`${row.size} B`:'—'}}</template></el-table-column>
   <el-table-column label="修改时间" min-width="180"><template #default="{row}">{{new Date(row.modified*1000).toLocaleString()}}</template></el-table-column>
   <el-table-column width="90"><template #default="{row}"><el-button v-if="row.kind==='file'" link :disabled="busy" @click="download(row)">下载</el-button></template></el-table-column>
  </el-table>
 </section>
</template>
<script setup>
import {ref,computed,onMounted,onBeforeUnmount} from 'vue'
import axios from 'axios'
const props=defineProps({deviceId:String,root:String,link:String})
const path=ref('.'),entries=ref([]),busy=ref(false),progress=ref(0),phase=ref(''),error=ref(''),retryFile=ref(null)
const absolutePath=computed(()=>path.value==='.'?props.root:`${props.root.replace(/\/$/,'')}/${path.value}`)
let disposed=false,cancelRequested=false,activeSocket=null
const api=async data=>(await axios.post(`/api/devices/${props.deviceId}/files`,data,{timeout:40000,params:{link:props.link||''}})).data
const message=e=>e.response?.data?.error||e.message||'操作失败'
const join=name=>path.value==='.'?name:`${path.value}/${name}`
async function navigate(target){error.value='';try{const r=await api({action:'list',path:target});if(!disposed){entries.value=r.entries;path.value=r.path}}catch(e){if(!disposed)error.value=message(e)}}
async function list(){await navigate(path.value)}
async function enter(name){await navigate(join(name))}
async function parent(){await navigate(path.value.includes('/')?path.value.split('/').slice(0,-1).join('/'):'.')}
function choose(e){const file=e.target.files[0];e.target.value='';if(file)upload(file)}
function cancelTransfer(){cancelRequested=true;if(activeSocket?.readyState===WebSocket.OPEN)activeSocket.send(JSON.stringify({type:'cancel'}));activeSocket?.close()}
const sha256=async bytes=>Array.from(new Uint8Array(await crypto.subtle.digest('SHA-256',bytes)),x=>x.toString(16).padStart(2,'0')).join('')
function binaryTransfer(direction,target,size,bytes,checksum){
 return new Promise((resolve,reject)=>{
  const query=new URLSearchParams({direction,path:target,link:props.link||''});if(direction==='upload'){query.set('size',size);query.set('sha256',checksum)}
  const ws=new WebSocket(`wss://${location.host}/api/devices/${props.deviceId}/file-transfer?${query}`);activeSocket=ws;ws.binaryType='arraybuffer'
  const chunks=[];let received=0,offset=0,credits=0,transferID='',settled=false,done=null
  const started=performance.now();const timer=setTimeout(()=>finish(Error('文件传输超时')),300000)
  function finish(error,result){if(settled)return;settled=true;clearTimeout(timer);if(activeSocket===ws)activeSocket=null;ws.close();if(error)reject(error);else resolve(result)}
  function update(n,total){progress.value=total?Math.min(99,Math.floor(n/total*100)):0;const elapsed=Math.max((performance.now()-started)/1000,.001);phase.value=`${direction==='upload'?'上传':'下载'} ${Math.round(n/1024)} KiB · ${(n/1048576/elapsed).toFixed(2)} MiB/s`}
  function pump(){while(credits>0&&offset<bytes.length&&ws.readyState===WebSocket.OPEN){const block=bytes.subarray(offset,offset+32768);const frame=new Uint8Array(block.length+2);frame.set([1,3]);frame.set(block,2);ws.send(frame);offset+=block.length;credits--;update(offset,size)}}
  ws.onmessage=async event=>{
   if(settled)return
   try{
    if(typeof event.data!=='string'){
     if(direction!=='download')throw Error('收到意外文件数据')
     const data=new Uint8Array(event.data);if(data[0]!==1)throw Error('无效二进制数据')
     if(data.length>1){const block=data.slice(1);received+=block.length;if(received>16*1024*1024)throw Error('文件超过大小限制');chunks.push(block);update(received,size);ws.send(JSON.stringify({type:'fileAck',transfer_id:transferID}))}
     return
    }
    const m=JSON.parse(event.data)
    if(m.type==='error'||m.type==='fileError')throw Error(m.error||'设备文件传输中断')
    if(m.type==='ready'){size=m.size;phase.value='文件检查完成，开始二进制传输'}
    if(m.type==='sendfile'){transferID=m.transfer_id;ws.send(JSON.stringify({type:'fileStart',transfer_id:transferID,window:16}))}
    if(m.type==='uploadReady'){
     credits=m.window
     if(!bytes.length)ws.send(new Uint8Array([1,3]));else pump()
    }
    if(m.type==='fileAck'&&direction==='upload'){credits++;pump()}
    if(m.type==='done'){
     done=m;phase.value='正在校验文件'
     if(direction==='download'){
      const blob=new Blob(chunks);if(received!==m.size)throw Error('下载文件长度不一致')
      const actual=await sha256(await blob.arrayBuffer());if(actual!==m.sha256)throw Error('下载 SHA-256 不一致')
      progress.value=100;finish(null,{blob,sha256:actual})
     }else{if(m.sha256!==checksum||m.size!==size)throw Error('上传校验不一致');progress.value=100;finish(null,m)}
    }
   }catch(e){finish(e)}
  }
  ws.onerror=()=>finish(Error('文件通道连接失败，请检查设备是否在线、系统账号是否匹配'))
  ws.onclose=()=>{if(!done&&!settled)finish(Error('文件通道已关闭或已取消；未确认成功，请刷新目录检查'))}
 })
}
async function upload(file){
 if(file.size>16*1024*1024){error.value='上传文件上限为 16 MiB';return}
 busy.value=true;cancelRequested=false;progress.value=0;error.value='';phase.value='正在计算文件校验值';retryFile.value=null
 try{const bytes=new Uint8Array(await file.arrayBuffer());const checksum=await sha256(bytes);if(disposed)return;if(cancelRequested)throw Error('传输已取消');await binaryTransfer('upload',join(file.name),file.size,bytes,checksum);phase.value='上传完成，SHA-256 校验一致';await list()}
 catch(e){if(!disposed){error.value=message(e);retryFile.value=file}}finally{busy.value=false}
}
async function download(row){
 if(row.size>16*1024*1024){error.value='下载文件上限为 16 MiB';return}
 busy.value=true;cancelRequested=false;progress.value=0;error.value='';phase.value='正在建立二进制文件通道'
 try{const result=await binaryTransfer('download',join(row.name),row.size);if(disposed)return;const url=URL.createObjectURL(result.blob);const a=document.createElement('a');a.href=url;a.download=row.name;a.click();setTimeout(()=>URL.revokeObjectURL(url),1000);phase.value='下载完成，SHA-256 校验一致'}
 catch(e){if(!disposed)error.value=message(e)}finally{busy.value=false}
}
onMounted(list);onBeforeUnmount(()=>{disposed=true;cancelTransfer()})

</script>
<style scoped>.file-controls{display:flex;align-items:center;gap:12px;margin:18px 0;flex-wrap:wrap}.file-controls code{flex:1;overflow-wrap:anywhere}.file-upload{border:1px solid #ddd;padding:7px 12px;border-radius:4px;cursor:pointer}.file-upload input{display:block;max-width:210px}.el-alert{margin-bottom:12px}</style>
