<template>
 <el-button v-if="enabled" @click="error='';open=true"><ConsoleIcon name="plus"/>新建版本</el-button>
 <el-drawer v-model="open" title="新建 PSDK 版本" size="min(580px,100vw)" class="console-drawer" :close-on-click-modal="!busy" :close-on-press-escape="!busy" :show-close="!busy">
  <p class="builder-intro">将程序镜像与机型配置制作成一个可发布版本。完成后，你可以在版本库中选择设备发布。</p>
  <el-alert v-if="error" :title="error" type="error" show-icon :closable="false"/>
  <el-form label-position="top" class="builder-form" @submit.prevent="create">
   <h3>版本标识</h3>
   <el-form-item label="版本号"><el-input v-model="version" :disabled="busy" placeholder="例如 v3.16.0-release1" maxlength="80"/><p class="field-help">同租户内不可重复；支持字母、数字、点、短横线和下划线。</p></el-form-item>
   <h3>部署内容</h3>
   <el-form-item label="目标镜像 ID"><el-input v-model="image" :disabled="busy" placeholder="sha256: 后跟 64 位十六进制"/><p class="field-help">填写完整镜像 ID，目标镜像需预先缓存到盒子。</p></el-form-item>
   <el-form-item label="程序 SHA-256"><el-input v-model="binary" :disabled="busy" placeholder="64 位十六进制摘要"/><p class="field-help">用于校验镜像内的 ja_UAV_PSDK 程序。</p></el-form-item>
   <el-form-item label="机型配置 uavModel"><label class="model-file"><ConsoleIcon name="folder"/><span>{{model?.name||'选择配置文件'}}<small>{{model?`${Math.ceil(model.size/1024)} KiB`:'非空 JSON · 最大 1 MiB'}}</small></span><input type="file" :disabled="busy" aria-label="选择 uavModel 文件" @change="choose"/></label></el-form-item>
  </el-form>
  <p class="builder-note">平台将自动计算模型摘要、签名并导入制品。创建版本不会立即升级设备。</p>
  <template #footer><el-button :disabled="busy" @click="open=false">取消</el-button><el-button type="primary" :loading="busy" @click="create">{{busy?'正在制作并导入…':'创建版本'}}</el-button></template>
 </el-drawer>
</template>
<script setup>
import {ref} from 'vue'
import axios from 'axios'
import ConsoleIcon from './ConsoleIcon.vue'
defineProps({enabled:Boolean});const emit=defineEmits(['created'])
const open=ref(false),busy=ref(false),version=ref(''),image=ref(''),binary=ref(''),model=ref(null),error=ref('')
function choose(e){model.value=e.target.files[0]||null}
async function create(){
 if(busy.value)return
 if(!/^[A-Za-z0-9][A-Za-z0-9._-]{0,79}$/.test(version.value)){error.value='请填写有效版本号';return}
 if(!/^sha256:[a-f0-9]{64}$/.test(image.value)||!/^[a-f0-9]{64}$/.test(binary.value)){error.value='请填写完整镜像 ID 和程序 SHA-256';return}
 if(!model.value||!model.value.size||model.value.size>1048576){error.value='请选择不超过 1 MiB 的非空 uavModel';return}
 const form=new FormData();form.append('version',version.value);form.append('image',image.value);form.append('binary_sha256',binary.value);form.append('model',model.value)
 busy.value=true;error.value=''
 try{const {data}=await axios.post('/api/ota/versions',form,{timeout:110000});open.value=false;emit('created',data)}catch(e){error.value=e.response?.data?.error||'制作结果未确认，请刷新版本记录后再操作'}finally{busy.value=false}
}
</script>

<style scoped>
.el-button>svg{width:14px;height:14px;margin-right:5px}.builder-intro{font-size:13px;line-height:1.9;color:#7a879b;margin:0 0 24px}.builder-form{margin-top:24px}.builder-form h3{font-size:12px;color:#909aad;font-weight:500;margin:28px 0 20px;padding-bottom:12px;border-bottom:1px solid #e9ecf2}.builder-form :deep(.el-form-item){margin-bottom:24px}.builder-form :deep(.el-form-item__label){font-size:13px;font-weight:550;color:#45536b}.field-help{font-size:11px;color:#8a95a7;line-height:1.8;margin:7px 0 0}.model-file{display:flex;align-items:center;position:relative;gap:12px;padding:18px 16px;border:1px dashed #cad2e2;border-radius:7px;width:100%;cursor:pointer;background:#fafbfe;line-height:1.8}.model-file:hover{border-color:#7f91c5}.model-file:focus-within{outline:2px solid #7185d2;outline-offset:2px}.model-file>svg{width:24px;height:24px;color:#8997b6;flex-shrink:0}.model-file>span{font-size:13px;color:#50617e;overflow-wrap:anywhere;min-width:0}.model-file small{display:block;font-size:11px;color:#8b97ab;margin-top:3px}.model-file input{position:absolute;inset:0;opacity:0;width:100%;cursor:pointer}.model-file:has(input:disabled){opacity:.6;cursor:default}.builder-note{font-size:12px;line-height:1.9;color:#8691a4;background:#f6f7fb;padding:16px;border-radius:6px;margin:28px 0 0}
</style>
