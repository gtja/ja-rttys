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
    <form id="login-form" action="/api/signin" method="post" autocomplete="on" @submit.prevent="handleSubmit">
      <div class="login-field-row">
        <label for="login-username">账号</label>
        <input id="login-username" name="username" class="login-field" type="text" autocomplete="username" autocapitalize="none" spellcheck="false" aria-label="用户名" placeholder="请输入用户名" required>
      </div>
      <div class="login-field-row">
        <label for="login-password">密码</label>
        <div class="password-field">
          <input id="login-password" name="password" class="login-field" :type="passwordVisible?'text':'password'" autocomplete="current-password" aria-label="密码" :placeholder="$t('Enter password...')" required>
          <button type="button" class="password-toggle" :aria-label="passwordVisible?'隐藏密码':'显示密码'" :aria-pressed="passwordVisible" @click="passwordVisible=!passwordVisible">{{passwordVisible?'隐藏':'显示'}}</button>
        </div>
      </div>
      <el-button type="primary" native-type="submit" :loading="loading" class="login-button">登录工作空间</el-button>
    </form>
    <p class="login-help">无法登录？请联系你的租户管理员。</p>
    </div>
   </section>
  </main>
</template>

<script setup>
import { ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { ElMessage } from 'element-plus'
import axios from 'axios'

const { t } = useI18n()

const loading = ref(false)
const passwordVisible = ref(false)

const handleSubmit = (event) => {
  if (loading.value) return
  // Read the native fields even when a password manager did not emit input events.
  const fields = new FormData(event.currentTarget)
  const params = {
    username: String(fields.get('username') || ''),
    password: String(fields.get('password') || '')
  }
  if (!params.username || !params.password) {
    ElMessage.error('请输入账号和密码')
    return
  }
  loading.value = true

  axios.post('/api/signin', params).then(() => {
    // A completed navigation gives password managers a clear successful-login signal.
    window.location.replace('/')
  }).catch(() => {
    ElMessage.error(t('Signin Fail! password wrong.'))
  }).finally(() => {
    loading.value = false
  })
}
</script>

<style scoped>
.login-field-row{margin-bottom:22px}.login-field-row label{display:block;font-size:12px;color:#657b6c;padding-bottom:9px}.login-field{box-sizing:border-box;width:100%;height:40px;padding:0 12px;border:1px solid #dce5dd;border-radius:7px;background:#fff;color:#273047;font:inherit;font-size:14px;outline:none}.login-field:focus{border-color:#455cd0;box-shadow:0 0 0 1px #455cd0}.login-field::placeholder{color:#a8abb2}.password-field{position:relative}.password-field input{padding-right:58px}.password-toggle{position:absolute;right:8px;top:0;height:40px;padding:0 5px;border:0;background:transparent;color:#657b6c;font-size:12px;cursor:pointer}.password-toggle:focus-visible{outline:2px solid #455cd0;border-radius:4px}

.login-page{--el-color-primary:#455cd0;--el-color-primary-dark-2:#3548a9;--el-color-primary-light-3:#7182db;display:grid;grid-template-columns:1fr 1fr;min-height:100dvh;background:#fbfcfb;font-family:"SF Pro Display","PingFang SC","Microsoft YaHei",sans-serif;color:#273047}
.login-story{padding:48px 64px;background:#eef1fa;display:flex;flex-direction:column;position:relative;overflow:hidden}.wordmark{font-size:24px;font-weight:700;letter-spacing:1px;display:flex;align-items:center;gap:13px}.wordmark span{display:grid;place-items:center;width:38px;height:38px;border-radius:9px;background:#455cd0;color:#fff;font-family:monospace}.story-content{margin:auto 0;padding:60px 0;position:relative;z-index:1}.overline{font-size:10px;letter-spacing:2px;color:#809689;margin-bottom:28px}h1{font-size:clamp(30px,3.5vw,54px);line-height:1.45;letter-spacing:-2px;font-weight:600;margin:0 0 24px}.story-content p{font-size:14px;color:#7b879e;line-height:2}.story-line{width:48px;height:2px;background:#9aa9d6;margin:38px 0}.story-features{display:flex;flex-wrap:wrap;gap:28px;font-size:12px;color:#7182a8}.login-story>small{font-size:11px;color:#90a397}.login-side{display:grid;place-items:center;padding:48px 32px}.login{width:min(360px,100%)}.login-kicker{font-size:11px;color:#8e9c93;letter-spacing:1px}h2{font-size:30px;font-weight:600;margin:15px 0 12px;letter-spacing:-1px}.login-description{font-size:13px;color:#8b988e;margin:0 0 36px}.login-button{width:100%;height:44px;margin-top:10px;border-radius:7px}.login-help{font-size:11px;text-align:center;color:#9aa79d;margin-top:28px}:deep(.el-form-item){display:block;margin-bottom:22px}:deep(.el-form-item__label){float:none;display:block;font-size:12px;color:#657b6c;padding-bottom:5px}:deep(.el-input__wrapper){border-radius:7px;box-shadow:0 0 0 1px #dce5dd inset}:deep(.el-form-item__content){margin-left:0!important}
@media(max-width:800px){.login-page{grid-template-columns:1fr}.login-story{padding:24px}.story-content{padding:30px 0}.story-content h1{font-size:30px}.story-line,.story-features,.login-story>small,.overline{display:none}.story-content p{margin-bottom:0}.login-side{padding:36px 24px 60px}}@media(prefers-reduced-motion:reduce){*{transition:none!important}}
</style>
