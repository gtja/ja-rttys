<template>
  <div class="presence-page" v-loading="loading">
    <div class="presence-toolbar">
      <el-radio-group v-model="days" @change="reset"><el-radio-button v-for="d in [1,7,30,90]" :key="d" :value="d">最近 {{d}} 天</el-radio-button></el-radio-group>
      <div><el-button v-if="device" @click="device='';reset()">返回全部设备</el-button><el-button @click="reset">刷新</el-button></div>
    </div>
    <el-alert v-if="error" :title="error" type="error" :closable="false"/>
    <p class="presence-note">{{device?'当前设备':'当前权限范围'}} · 统计窗口 {{stamp(data.since)}} — {{stamp(data.until)}}（浏览器本地时间）。历史从 {{stamp(data.recording_since)}} 开始记录，每 30 秒刷新。</p>
    <div class="presence-metrics">
      <div v-for="m in metrics" :key="m.label" class="surface"><span>{{m.label}}</span><strong>{{m.value}}</strong><small>{{m.note}}</small></div>
    </div>
    <section class="surface presence-section">
      <div class="presence-heading"><div><h2>最近上线的设备</h2><p>按最近一次上线时间倒序，展示窗口内最近上线的 100 台设备。</p></div></div>
      <el-table :data="data.recent||[]" empty-text="所选时间内暂无上线记录" row-key="id">
        <el-table-column label="设备" min-width="170"><template #default="{row}"><b>{{row.name}}</b><div class="secondary">{{row.wire_id}}</div></template></el-table-column>
        <el-table-column prop="tenant_name" label="租户" min-width="120"/>
        <el-table-column label="当前状态" width="100"><template #default="{row}">{{row.disabled?'停用':row.online?'在线':'离线'}}</template></el-table-column>
        <el-table-column label="最近上线" min-width="180"><template #default="{row}">{{stamp(row.last_online)}}</template></el-table-column>
        <el-table-column label="最近确认离线" min-width="180"><template #default="{row}">{{stamp(row.last_offline)}}</template></el-table-column>
        <el-table-column width="100"><template #default="{row}"><el-button link type="primary" @click="device=row.id;reset()">查看历史</el-button></template></el-table-column>
      </el-table>
    </section>
    <section class="surface presence-section">
      <div class="presence-heading"><div><h2>每次连接记录</h2><p>断电、网络中断以服务端检测时间为准。服务重启记录只计至末次持久化观测，不当作准确离线时间。</p></div></div>
      <el-table :data="events" empty-text="暂无连接记录" row-key="id">
        <el-table-column label="设备 / 租户" min-width="180"><template #default="{row}">{{row.name}}<div class="secondary">{{row.wire_id}} · {{row.tenant_name}}</div></template></el-table-column>
        <el-table-column label="上线时间" min-width="180"><template #default="{row}">{{stamp(row.online_at)}}</template></el-table-column>
        <el-table-column prop="link" label="链路" width="90"/>
        <el-table-column label="离线时间" min-width="200"><template #default="{row}"><template v-if="row.reason==='server_restart'">时间不确定<div class="secondary">末次观测 {{stamp(row.last_seen)}}</div></template><template v-else>{{row.offline_at?stamp(row.offline_at):'仍在线'}}</template></template></el-table-column>
        <el-table-column label="本次已观测时长" min-width="150"><template #default="{row}">{{duration(row.duration_seconds)}}</template></el-table-column>
        <el-table-column label="连接结束原因" min-width="150"><template #default="{row}">{{reasons[row.reason]||'进行中'}}</template></el-table-column>
      </el-table>
      <div class="presence-footer"><el-button v-if="cursor" :loading="loading" @click="load(true)">加载更早记录</el-button><span v-else class="secondary">已展示当前范围内全部记录</span></div>
    </section>
    <section class="surface presence-section">
      <div class="presence-heading"><div><h2>每日上下线统计</h2><p>按上海时间汇总。有记录的日期显示如下；无记录日期次数为 0。</p></div></div>
      <el-table :data="data.daily||[]" empty-text="暂无日统计"><el-table-column prop="day" label="日期"/><el-table-column prop="connections" label="上线次数"/><el-table-column prop="disconnections" label="确认断连次数"/><el-table-column prop="uncertain" label="重启中断次数"/></el-table>
    </section>
  </div>
</template>
<script setup>
import {ref,computed,onMounted,onBeforeUnmount} from 'vue'
import axios from 'axios'
const days=ref(7),device=ref(''),data=ref({}),events=ref([]),cursor=ref(null),loading=ref(false),error=ref('')
let timer,sequence=0,disposed=false
const reasons={connection_closed:'连接关闭',heartbeat_timeout:'心跳超时',server_restart:'服务重启，时间不确定'}
const stamp=s=>s?new Date(s*1000).toLocaleString():'—'
function duration(s){s=Math.max(0,Math.floor(s||0));return s>=86400?`${Math.floor(s/86400)}天 ${Math.floor(s%86400/3600)}小时`:s>=3600?`${Math.floor(s/3600)}小时 ${Math.floor(s%3600/60)}分`:s>=60?`${Math.floor(s/60)}分 ${s%60}秒`:`${s}秒`}
const metrics=computed(()=>{const s=data.value.summary||{};return [
 {label:'设备 / 在线 / 离线',value:`${s.devices||0} / ${s.online||0} / ${Math.max(0,(s.devices||0)-(s.online||0))}`,note:`停用 ${s.disabled||0} 台；当前在线占比 ${s.devices?((s.online/s.devices)*100).toFixed(1):'0'}%`},
 {label:'上线次数',value:s.connections||0,note:'窗口内成功认证的连接次数'},
 {label:'确认断连次数',value:s.disconnections||0,note:`另有 ${s.uncertain||0} 次服务重启中断`},
 {label:'链路累计在线时长',value:duration(s.online_seconds),note:'各链路时长之和；多链路同时在线分别计时'},
 {label:'平均单次观测时长',value:duration(s.observed_sessions?s.online_seconds/s.observed_sessions:0),note:'包含未结束连接及窗口内的连接片段'},
 {label:'最长单次观测时长',value:duration(s.longest_seconds),note:'只计算统计窗口内的部分'}
]})
async function load(append=false){const seq=++sequence;loading.value=true;error.value='';try{const {data:d}=await axios.get('/api/presence',{params:{days:days.value,device_id:device.value||undefined,before:append?cursor.value:undefined}});if(disposed||seq!==sequence)return;data.value=d;events.value=append?[...events.value,...d.events]:d.events;cursor.value=d.next_before}catch(e){if(!disposed&&seq===sequence)error.value=e.response?.status===404?'设备已不可见，请返回全部设备':`读取失败（${e.response?.status||'网络异常'}）`}finally{if(!disposed&&seq===sequence)loading.value=false}}
function reset(){cursor.value=null;load()}
onMounted(()=>{load();timer=setInterval(()=>{if(!loading.value)load()},30000)})
onBeforeUnmount(()=>{disposed=true;sequence++;clearInterval(timer)})
</script>
<style scoped>
.presence-toolbar,.presence-heading{display:flex;justify-content:space-between;gap:16px;align-items:center}.presence-toolbar{flex-wrap:wrap}.presence-note,.presence-heading p{font-size:13px;color:#64748b;line-height:1.6}.presence-metrics{display:grid;grid-template-columns:repeat(3,minmax(0,1fr));gap:14px;margin:20px 0}.presence-metrics>div{padding:20px;display:flex;flex-direction:column;gap:10px}.presence-metrics span{color:#64748b;font-size:13px}.presence-metrics strong{font-size:25px;font-weight:600}.presence-metrics small{color:#64748b}.presence-section{margin-top:20px;overflow:hidden}.presence-heading{padding:20px 22px 8px}.presence-heading h2{font-size:17px;margin:0}.presence-heading p{margin:8px 0}.presence-footer{padding:16px;text-align:center}@media(max-width:900px){.presence-metrics{grid-template-columns:repeat(2,minmax(0,1fr))}}@media(max-width:560px){.presence-metrics{grid-template-columns:1fr}}
</style>
