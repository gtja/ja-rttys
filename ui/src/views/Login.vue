<template>
  <main class="login-page">
   <section class="login-story">
    <div class="wordmark"><span>›_</span> RTTY</div>
    <div class="story-content"><div class="overline">REMOTE OPERATIONS</div><h1>连接设备，<br>让维护井然有序。</h1><p>在独立的租户空间中组织设备、管理成员，<br>为每一次远程访问明确权限边界。</p><div class="story-line"></div><div class="story-features"><span>设备与分组</span><span>精细访问授权</span><span>远程终端</span></div></div>
    <small>RTTY · 远程运维控制台</small>
   </section>
   <section class="login-side">
    <div class="login">
    <div class="login-kicker">工作空间登录</div><h2>欢迎回来</h2><p class="login-description">使用管理员分配的账号访问设备。</p>
    <el-form :model="formValue" size="large" @submit.prevent="handleSubmit">
      <el-form-item prop="username" label="账号"><el-input v-model="formValue.username" aria-label="用户名" placeholder="请输入用户名" autocomplete="username" /></el-form-item>
      <el-form-item prop="password" label="密码">
        <el-input autofocus type="password" v-model="formValue.password" prefix-icon="lock" :placeholder="$t('Enter password...')" show-password/>
      </el-form-item>
      <el-form-item>
        <el-button type="primary" native-type="submit" :loading="loading" class="login-button">登录工作空间</el-button>
      </el-form-item>
    </el-form>
    <p class="login-help">无法登录？请联系你的租户管理员。</p>
    </div>
   </section>
  </main>
</template>

<script setup>
import { ref, reactive } from 'vue'
import { useRouter } from 'vue-router'
import { useI18n } from 'vue-i18n'
import { ElMessage } from 'element-plus'
import axios from 'axios'

const { t } = useI18n()
const router = useRouter()

const loading = ref(false)
const formValue = reactive({
  username: '',
  password: ''
})

const handleSubmit = () => {
  if (loading.value) return
  if (!formValue.username || !formValue.password) {
    ElMessage.error('请输入账号和密码')
    return
  }
  loading.value = true
  const params = {
    username: formValue.username,
    password: formValue.password
  }

  axios.post('/api/signin', params).then(() => {
    router.push('/')
  }).catch(() => {
    ElMessage.error(t('Signin Fail! password wrong.'))
  }).finally(() => {
    loading.value = false
  })
}
</script>

<style scoped>
.login-page{--el-color-primary:#27695a;--el-color-primary-dark-2:#205548;--el-color-primary-light-3:#51897c;display:grid;grid-template-columns:1fr 1fr;min-height:100dvh;background:#fbfcfb;font-family:"SF Pro Display","PingFang SC","Microsoft YaHei",sans-serif;color:#293d32}
.login-story{padding:48px 64px;background:#edf3ee;display:flex;flex-direction:column;position:relative;overflow:hidden}.wordmark{font-size:24px;font-weight:700;letter-spacing:1px;display:flex;align-items:center;gap:13px}.wordmark span{display:grid;place-items:center;width:38px;height:38px;border-radius:9px;background:#27695a;color:#fff;font-family:monospace}.story-content{margin:auto 0;padding:60px 0;position:relative;z-index:1}.overline{font-size:10px;letter-spacing:2px;color:#809689;margin-bottom:28px}h1{font-size:clamp(30px,3.5vw,54px);line-height:1.45;letter-spacing:-2px;font-weight:600;margin:0 0 24px}.story-content p{font-size:14px;color:#7c8f82;line-height:2}.story-line{width:48px;height:2px;background:#90b09c;margin:38px 0}.story-features{display:flex;flex-wrap:wrap;gap:28px;font-size:12px;color:#617d6c}.login-story>small{font-size:11px;color:#90a397}.login-side{display:grid;place-items:center;padding:48px 32px}.login{width:min(360px,100%)}.login-kicker{font-size:11px;color:#8e9c93;letter-spacing:1px}h2{font-size:30px;font-weight:600;margin:15px 0 12px;letter-spacing:-1px}.login-description{font-size:13px;color:#8b988e;margin:0 0 36px}.login-button{width:100%;height:44px;margin-top:10px;border-radius:7px}.login-help{font-size:11px;text-align:center;color:#9aa79d;margin-top:28px}:deep(.el-form-item){display:block;margin-bottom:22px}:deep(.el-form-item__label){float:none;display:block;font-size:12px;color:#657b6c;padding-bottom:5px}:deep(.el-input__wrapper){border-radius:7px;box-shadow:0 0 0 1px #dce5dd inset}:deep(.el-form-item__content){margin-left:0!important}
@media(max-width:800px){.login-page{grid-template-columns:1fr}.login-story{padding:24px}.story-content{padding:30px 0}.story-content h1{font-size:30px}.story-line,.story-features,.login-story>small,.overline{display:none}.story-content p{margin-bottom:0}.login-side{padding:36px 24px 60px}}@media(prefers-reduced-motion:reduce){*{transition:none!important}}
</style>
