<script setup lang="ts">
import { ref, onMounted } from 'vue'
import { useI18n } from 'vue-i18n'
import { EventsOn } from '../../wailsjs/runtime/runtime'

const { t, locale } = useI18n()

const cfdInstalled = ref(false)
const cfdVersion = ref('')
const installing = ref(false)
const installStatus = ref('')
const checking = ref(false)
const updating = ref(false)
const latestVersion = ref('')
const needUpdate = ref(false)
const updateStatus = ref('')

// 代理相关
const proxyEnabled = ref(false)
const proxyType = ref('http')
const proxyHost = ref('')
const proxyPort = ref('')
const proxyUsername = ref('')
const proxyPassword = ref('')
const proxySaved = ref(false)

// 开机自启动
const launchAtLogin = ref(false)

// 断连通知
const notifyDisconnect = ref(false)

async function loadCfdStatus() {
  const App = await import('../../wailsjs/go/main/App')
  cfdInstalled.value = await App.IsCloudflaredInstalled()
  if (cfdInstalled.value) {
    cfdVersion.value = await App.GetCloudflaredVersion()
  }
}

async function loadProxySettings() {
  const App = await import('../../wailsjs/go/main/App')
  proxyEnabled.value = (await App.GetSetting('proxy_enabled')) === 'true'
  proxyType.value = (await App.GetSetting('proxy_type')) || 'http'
  proxyHost.value = (await App.GetSetting('proxy_host')) || ''
  proxyPort.value = (await App.GetSetting('proxy_port')) || ''
  proxyUsername.value = (await App.GetSetting('proxy_username')) || ''
  proxyPassword.value = (await App.GetSetting('proxy_password')) || ''
}

async function loadLaunchAtLogin() {
  try {
    const App = await import('../../wailsjs/go/main/App')
    launchAtLogin.value = await App.IsAutoStartEnabled()
  } catch {}
}

async function loadNotifySettings() {
  const App = await import('../../wailsjs/go/main/App')
  notifyDisconnect.value = (await App.GetSetting('notify_disconnect')) === 'true'
}

async function toggleLaunchAtLogin() {
  try {
    const App = await import('../../wailsjs/go/main/App')
    await App.SetAutoStart(launchAtLogin.value)
  } catch {
    // 失败时回滚
    launchAtLogin.value = !launchAtLogin.value
  }
}

async function toggleNotifyDisconnect() {
  const App = await import('../../wailsjs/go/main/App')
  await App.SetSetting('notify_disconnect', notifyDisconnect.value ? 'true' : 'false')
}

// 构建代理 URL
function buildProxyURL(): string {
  if (!proxyHost.value || !proxyPort.value) return ''
  const scheme = proxyType.value === 'socks5' ? 'socks5' : 'http'
  let auth = ''
  if (proxyUsername.value) {
    auth = proxyPassword.value
      ? `${proxyUsername.value}:${proxyPassword.value}@`
      : `${proxyUsername.value}@`
  }
  return `${scheme}://${auth}${proxyHost.value}:${proxyPort.value}`
}

async function saveProxy() {
  const App = await import('../../wailsjs/go/main/App')
  await App.SetSetting('proxy_enabled', proxyEnabled.value ? 'true' : 'false')
  await App.SetSetting('proxy_type', proxyType.value)
  await App.SetSetting('proxy_host', proxyHost.value)
  await App.SetSetting('proxy_port', proxyPort.value)
  await App.SetSetting('proxy_username', proxyUsername.value)
  await App.SetSetting('proxy_password', proxyPassword.value)
  // 保存完整代理 URL 方便后端直接读取
  await App.SetSetting('proxy_url', proxyEnabled.value ? buildProxyURL() : '')
  proxySaved.value = true
  setTimeout(() => { proxySaved.value = false }, 2000)
}

async function installCfd() {
  installing.value = true
  installStatus.value = t('cfd.downloading')
  try {
    const App = await import('../../wailsjs/go/main/App')
    await App.InstallCloudflared()
    await loadCfdStatus()
    installStatus.value = t('cfd.done')
  } catch (e: any) {
    installStatus.value = t('cfd.failed') + ': ' + (e?.message || e)
  } finally {
    installing.value = false
  }
}

async function checkUpdate() {
  checking.value = true
  try {
    const App = await import('../../wailsjs/go/main/App')
    const result = await App.CheckCloudflaredUpdate()
    latestVersion.value = result.latest
    needUpdate.value = result.needUpdate === 'true'
    if (!needUpdate.value) {
      updateStatus.value = t('cfd.upToDate')
    }
  } catch (e: any) {
    updateStatus.value = t('cfd.checkFailed') + ': ' + (e?.message || e)
  } finally {
    checking.value = false
  }
}

async function updateCfd() {
  updating.value = true
  updateStatus.value = t('cfd.updating')
  try {
    const App = await import('../../wailsjs/go/main/App')
    await App.UpdateCloudflared()
    await loadCfdStatus()
    needUpdate.value = false
    updateStatus.value = t('cfd.updateDone')
  } catch (e: any) {
    updateStatus.value = t('cfd.updateFailed') + ': ' + (e?.message || e)
  } finally {
    updating.value = false
  }
}

async function changeLang(lang: string) {
  locale.value = lang
  const App = await import('../../wailsjs/go/main/App')
  await App.SetSetting('language', lang)
}

onMounted(() => {
  loadCfdStatus()
  loadProxySettings()
  loadLaunchAtLogin()
  loadNotifySettings()
  EventsOn('install-progress', (status: string) => {
    if (status === 'downloading') installStatus.value = t('cfd.downloading')
    else if (status === 'extracting') installStatus.value = t('cfd.extracting')
    else if (status === 'done') installStatus.value = t('cfd.done')
  })
})
</script>

<template>
  <div class="settings">
    <h2>{{ t('settings.title') }}</h2>

    <div class="section">
      <h3>{{ t('settings.language') }}</h3>
      <div class="lang-switcher">
        <button
          :class="['btn-secondary', { active: locale === 'zh' }]"
          @click="changeLang('zh')"
        >中文</button>
        <button
          :class="['btn-secondary', { active: locale === 'en' }]"
          @click="changeLang('en')"
        >English</button>
      </div>
    </div>

    <!-- 通用开关 -->
    <div class="section">
      <div class="setting-item">
        <div class="setting-info">
          <span class="setting-title">{{ t('settings.launchAtLogin') }}</span>
          <span class="setting-desc">{{ t('settings.launchAtLoginDesc') }}</span>
        </div>
        <label class="switch">
          <input type="checkbox" v-model="launchAtLogin" @change="toggleLaunchAtLogin" />
          <span class="slider"></span>
        </label>
      </div>
      <div class="setting-item">
        <div class="setting-info">
          <span class="setting-title">{{ t('settings.notifyDisconnect') }}</span>
          <span class="setting-desc">{{ t('settings.notifyDisconnectDesc') }}</span>
        </div>
        <label class="switch">
          <input type="checkbox" v-model="notifyDisconnect" @change="toggleNotifyDisconnect" />
          <span class="slider"></span>
        </label>
      </div>
    </div>

    <!-- 代理设置 -->
    <div class="section">
      <h3>{{ t('settings.proxy') }}</h3>

      <div class="proxy-toggle">
        <label class="switch-label">
          <span>{{ t('settings.proxyEnabled') }}</span>
          <label class="switch">
            <input type="checkbox" v-model="proxyEnabled" @change="saveProxy" />
            <span class="slider"></span>
          </label>
        </label>
      </div>

      <div v-if="proxyEnabled" class="proxy-form">
        <div class="proxy-hint">{{ t('settings.proxyHint') }}</div>

        <div class="form-row">
          <label>{{ t('settings.proxyType') }}</label>
          <div class="type-switcher">
            <button
              :class="['btn-secondary', { active: proxyType === 'http' }]"
              @click="proxyType = 'http'"
            >HTTP</button>
            <button
              :class="['btn-secondary', { active: proxyType === 'socks5' }]"
              @click="proxyType = 'socks5'"
            >SOCKS5</button>
          </div>
        </div>

        <div class="form-row host-port">
          <div class="host-field">
            <label>{{ t('settings.proxyHost') }}</label>
            <input
              type="text"
              v-model="proxyHost"
              :placeholder="t('settings.proxyPlaceholder')"
            />
          </div>
          <div class="port-field">
            <label>{{ t('settings.proxyPort') }}</label>
            <input
              type="number"
              v-model="proxyPort"
              :placeholder="t('settings.proxyPortPlaceholder')"
            />
          </div>
        </div>

        <div class="form-row host-port">
          <div class="host-field">
            <label>{{ t('settings.proxyUsername') }}</label>
            <input type="text" v-model="proxyUsername" placeholder="" />
          </div>
          <div class="port-field">
            <label>{{ t('settings.proxyPassword') }}</label>
            <input type="password" v-model="proxyPassword" placeholder="" />
          </div>
        </div>

        <div class="proxy-actions">
          <button class="btn-primary" @click="saveProxy">
            {{ proxySaved ? t('settings.proxySaved') : t('settings.proxySave') }}
          </button>
          <span v-if="proxyHost && proxyPort" class="proxy-preview">
            {{ buildProxyURL() }}
          </span>
        </div>
      </div>
    </div>

    <div class="section">
      <h3>{{ t('settings.cfdStatus') }}</h3>
      <div class="cfd-status">
        <div v-if="cfdInstalled" class="status-row">
          <span class="badge badge-success">{{ t('cfd.installed') }}</span>
          <span class="version" v-if="cfdVersion">{{ t('cfd.version') }}: {{ cfdVersion }}</span>
        </div>
        <div v-else class="status-row">
          <span class="badge badge-muted">{{ t('cfd.notInstalled') }}</span>
        </div>

        <div v-if="cfdInstalled" class="update-actions">
          <button class="btn-secondary" :disabled="checking" @click="checkUpdate">
            {{ checking ? t('cfd.checking') : t('cfd.checkUpdate') }}
          </button>
          <button v-if="needUpdate" class="btn-primary" :disabled="updating" @click="updateCfd">
            {{ updating ? t('cfd.updating') : t('cfd.updateTo') + ' ' + latestVersion }}
          </button>
        </div>

        <button
          v-if="!cfdInstalled"
          class="btn-primary"
          :disabled="installing"
          @click="installCfd"
        >
          {{ installing ? t('cfd.installing') : t('cfd.install') }}
        </button>
        <div v-if="installStatus || updateStatus" class="install-status">{{ installStatus || updateStatus }}</div>
      </div>
    </div>

    <div class="section">
      <h3>{{ t('settings.about') }}</h3>
      <p class="about-text">{{ t('settings.aboutDesc') }}</p>
    </div>
  </div>
</template>

<style scoped>
.settings {
  max-width: 600px;
  margin: 0 auto;
}

.settings h2 {
  font-size: 20px;
  font-weight: 600;
  margin-bottom: 24px;
}

.section {
  background: var(--bg-secondary);
  border: 1px solid var(--border);
  border-radius: 10px;
  padding: 20px;
  margin-bottom: 16px;
}

.section h3 {
  font-size: 14px;
  color: var(--text-secondary);
  margin-bottom: 12px;
  font-weight: 500;
}

.lang-switcher {
  display: flex;
  gap: 8px;
}

.lang-switcher .active {
  background: var(--accent);
  color: #ffffff;
  font-weight: 600;
}

/* 设置项行 */
.setting-item {
  display: flex;
  align-items: center;
  justify-content: space-between;
  padding: 10px 0;
}
.setting-item + .setting-item {
  border-top: 1px solid var(--border);
}
.setting-info {
  display: flex;
  flex-direction: column;
  gap: 2px;
}
.setting-title {
  font-size: 14px;
  font-weight: 500;
  color: var(--text-primary);
}
.setting-desc {
  font-size: 12px;
  color: var(--text-muted);
}

/* 代理设置样式 */
.proxy-toggle {
  margin-bottom: 12px;
}

.switch-label {
  display: flex;
  align-items: center;
  justify-content: space-between;
  font-size: 14px;
  cursor: pointer;
}

.switch {
  position: relative;
  display: inline-block;
  width: 44px;
  height: 24px;
  flex-shrink: 0;
}

.switch input {
  opacity: 0;
  width: 0;
  height: 0;
}

.slider {
  position: absolute;
  cursor: pointer;
  top: 0;
  left: 0;
  right: 0;
  bottom: 0;
  background: var(--border);
  border-radius: 24px;
  transition: 0.2s;
}

.slider::before {
  content: '';
  position: absolute;
  height: 18px;
  width: 18px;
  left: 3px;
  bottom: 3px;
  background: white;
  border-radius: 50%;
  transition: 0.2s;
}

.switch input:checked + .slider {
  background: var(--accent);
}

.switch input:checked + .slider::before {
  transform: translateX(20px);
}

.proxy-form {
  display: flex;
  flex-direction: column;
  gap: 14px;
}

.proxy-hint {
  font-size: 12px;
  color: var(--accent);
  background: rgba(0, 122, 255, 0.08);
  padding: 8px 12px;
  border-radius: 6px;
  line-height: 1.5;
}

.form-row {
  display: flex;
  flex-direction: column;
  gap: 6px;
}

.form-row label {
  font-size: 13px;
  color: var(--text-secondary);
}

.type-switcher {
  display: flex;
  gap: 8px;
}

.type-switcher .active {
  background: var(--accent);
  color: #ffffff;
  font-weight: 600;
}

.host-port {
  flex-direction: row;
  gap: 12px;
}

.host-field {
  flex: 1;
  display: flex;
  flex-direction: column;
  gap: 6px;
}

.port-field {
  width: 100px;
  display: flex;
  flex-direction: column;
  gap: 6px;
}

.host-field input,
.port-field input {
  width: 100%;
  padding: 8px 12px;
  border: 1px solid var(--border);
  border-radius: 8px;
  font-size: 14px;
  background: var(--bg-primary);
  color: var(--text-primary);
  box-sizing: border-box;
}

.host-field input:focus,
.port-field input:focus {
  outline: none;
  border-color: var(--accent);
}

/* 去除 number input 箭头 */
.port-field input[type="number"]::-webkit-inner-spin-button,
.port-field input[type="number"]::-webkit-outer-spin-button {
  -webkit-appearance: none;
  margin: 0;
}

.proxy-actions {
  display: flex;
  align-items: center;
  gap: 12px;
}

.proxy-preview {
  font-size: 12px;
  color: var(--text-secondary);
  font-family: 'SF Mono', Monaco, monospace;
  word-break: break-all;
}

.cfd-status {
  display: flex;
  flex-direction: column;
  gap: 10px;
}

.status-row {
  display: flex;
  align-items: center;
  gap: 12px;
}

.version {
  font-size: 13px;
  color: var(--text-secondary);
  font-family: 'SF Mono', Monaco, monospace;
}

.install-status {
  font-size: 13px;
  color: var(--text-secondary);
}

.about-text {
  font-size: 14px;
  color: var(--text-secondary);
}

.update-actions {
  display: flex;
  gap: 8px;
  align-items: center;
}
</style>
