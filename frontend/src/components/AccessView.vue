<script setup lang="ts">
import { ref, onMounted, onUnmounted, nextTick } from 'vue'
import { useI18n } from 'vue-i18n'

const { t } = useI18n()

interface Access {
  id: number
  name: string
  hostname: string
  localPort: number
  serviceTokenId: string
  serviceTokenSecret: string
  autoStart: boolean
  createdAt: string
  updatedAt: string
}

interface AccessStatus {
  running: boolean
  error: string
  lastLog: string
}

const accesses = ref<Access[]>([])
const statuses = ref<Record<number, AccessStatus>>({})
const loading = ref(true)
const cfdInstalled = ref(false)
const showForm = ref(false)
const editingAccess = ref<Access | null>(null)

// 日志查看器
const showLogs = ref(false)
const logAccessId = ref(0)
const logAccessName = ref('')
const logLines = ref<string[]>([])
let logPollTimer: number | null = null

let pollTimer: number | null = null

const formData = ref({
  name: '',
  hostname: '',
  localPort: 2222,
  serviceTokenId: '',
  serviceTokenSecret: '',
  autoStart: false,
})

// 根据端口自动生成使用提示
function usageHint(port: number): string {
  if (port === 22 || port === 2222) return `ssh user@localhost -p ${port}`
  if (port === 3389) return `mstsc /v:localhost:${port}`
  if (port === 5432) return `psql -h localhost -p ${port}`
  if (port === 3306) return `mysql -h 127.0.0.1 -P ${port}`
  if (port === 6379) return `redis-cli -p ${port}`
  if (port === 27017) return `mongosh --port ${port}`
  return `localhost:${port}`
}

function copyText(text: string) {
  navigator.clipboard.writeText(text)
}

function getStatus(id: number): AccessStatus {
  return statuses.value[id] || { running: false, error: '', lastLog: '' }
}

async function loadData() {
  try {
    const App = await import('../../wailsjs/go/main/App')
    cfdInstalled.value = await App.IsCloudflaredInstalled()
    accesses.value = await App.GetAccesses()
    statuses.value = await App.GetAllAccessStatuses()
  } catch (e) {
    console.error(e)
  } finally {
    loading.value = false
  }
}

async function pollStatuses() {
  try {
    const App = await import('../../wailsjs/go/main/App')
    statuses.value = await App.GetAllAccessStatuses()
  } catch {}
}

function openCreate() {
  editingAccess.value = null
  formData.value = { name: '', hostname: '', localPort: 2222, serviceTokenId: '', serviceTokenSecret: '', autoStart: false }
  showForm.value = true
}

function openEdit(a: Access) {
  editingAccess.value = a
  formData.value = {
    name: a.name,
    hostname: a.hostname,
    localPort: a.localPort,
    serviceTokenId: a.serviceTokenId || '',
    serviceTokenSecret: a.serviceTokenSecret || '',
    autoStart: a.autoStart || false,
  }
  showForm.value = true
}

async function saveForm() {
  const App = await import('../../wailsjs/go/main/App')
  const { name, hostname, localPort, serviceTokenId, serviceTokenSecret, autoStart } = formData.value
  if (editingAccess.value) {
    await App.UpdateAccess(editingAccess.value.id, name, hostname, localPort, serviceTokenId, serviceTokenSecret, autoStart)
  } else {
    await App.CreateAccess(name, hostname, localPort, serviceTokenId, serviceTokenSecret, autoStart)
  }
  showForm.value = false
  await loadData()
}

async function deleteAccess(id: number) {
  if (!confirm(t('access.confirmDelete'))) return
  const App = await import('../../wailsjs/go/main/App')
  await App.DeleteAccess(id)
  await loadData()
}

async function startAccess(id: number) {
  const App = await import('../../wailsjs/go/main/App')
  await App.StartAccess(id)
  setTimeout(pollStatuses, 500)
}

async function stopAccess(id: number) {
  const App = await import('../../wailsjs/go/main/App')
  await App.StopAccess(id)
  setTimeout(pollStatuses, 500)
}

// 日志相关
function isScrolledToBottom(el: Element): boolean {
  return el.scrollHeight - el.scrollTop - el.clientHeight < 30
}

async function fetchLogs() {
  try {
    const App = await import('../../wailsjs/go/main/App')
    const el = document.querySelector('.log-content')
    const wasAtBottom = !el || isScrolledToBottom(el)
    logLines.value = await App.GetAccessLogs(logAccessId.value) || []
    if (wasAtBottom) {
      await nextTick()
      if (el) el.scrollTop = el.scrollHeight
    }
  } catch {}
}

async function openLogViewer(a: Access) {
  logAccessId.value = a.id
  logAccessName.value = a.name
  showLogs.value = true
  await fetchLogs()
  logPollTimer = window.setInterval(fetchLogs, 1000)
}

function closeLogViewer() {
  showLogs.value = false
  if (logPollTimer) {
    clearInterval(logPollTimer)
    logPollTimer = null
  }
}

function copyAllLogs() {
  copyText(logLines.value.join('\n'))
}

onMounted(() => {
  loadData()
  pollTimer = window.setInterval(pollStatuses, 2000)
})

onUnmounted(() => {
  if (pollTimer) clearInterval(pollTimer)
  if (logPollTimer) clearInterval(logPollTimer)
})
</script>

<template>
  <div class="access-view">
    <!-- 工具栏 -->
    <div class="toolbar">
      <h2>{{ t('access.title') }}</h2>
      <button class="btn-primary" @click="openCreate">+ {{ t('access.create') }}</button>
    </div>

    <!-- 使用说明 -->
    <div class="guide-box">
      <p class="guide-title">{{ t('access.guide') }}</p>
      <ol>
        <li>{{ t('access.guideStep1') }}</li>
        <li>{{ t('access.guideStep2') }}</li>
        <li>{{ t('access.guideStep3') }}</li>
      </ol>
    </div>

    <div v-if="!cfdInstalled && !loading" class="notice">
      {{ t('cfd.notInstalled') }} - {{ t('nav.settings') }}
    </div>

    <div v-if="loading" class="empty">Loading...</div>

    <div v-else-if="accesses.length === 0" class="empty">
      {{ t('access.noAccesses') }}
    </div>

    <!-- 访问隧道卡片列表 -->
    <div v-else class="cards">
      <div v-for="acc in accesses" :key="acc.id" class="card">
        <div class="card-header">
          <div class="card-title">
            <span class="card-name">{{ acc.name }}</span>
            <span :class="['badge', getStatus(acc.id).running ? 'badge-success' : 'badge-muted']">
              {{ getStatus(acc.id).running ? t('access.connected') : t('access.disconnected') }}
            </span>
            <span v-if="acc.autoStart" class="badge badge-auto">{{ t('access.autoStart') }}</span>
          </div>
          <div class="card-actions">
            <button
              v-if="!getStatus(acc.id).running"
              class="btn-success btn-sm"
              :disabled="!cfdInstalled"
              @click="startAccess(acc.id)"
            >{{ t('access.connect') }}</button>
            <button
              v-else
              class="btn-danger btn-sm"
              @click="stopAccess(acc.id)"
            >{{ t('access.disconnect') }}</button>
            <button class="btn-secondary btn-sm" @click="openEdit(acc)">{{ t('tunnel.edit') }}</button>
            <button class="btn-danger btn-sm" @click="deleteAccess(acc.id)">{{ t('tunnel.delete') }}</button>
          </div>
        </div>
        <div class="card-body">
          <!-- 路由信息 -->
          <div class="card-route">
            <span class="route-hostname">{{ acc.hostname }}</span>
            <span class="route-arrow">--&gt;</span>
            <span class="route-local">localhost:{{ acc.localPort }}</span>
          </div>
          <!-- 使用方式 -->
          <div class="card-usage">
            <span class="usage-label">{{ t('access.usageHint') }}:</span>
            <code class="usage-cmd">{{ usageHint(acc.localPort) }}</code>
            <button class="btn-icon btn-sm" @click="copyText(usageHint(acc.localPort))" :title="t('common.copy')">
              <svg width="13" height="13" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
                <rect x="9" y="9" width="13" height="13" rx="2" ry="2"></rect>
                <path d="M5 15H4a2 2 0 01-2-2V4a2 2 0 012-2h9a2 2 0 012 2v1"></path>
              </svg>
            </button>
          </div>
          <!-- 错误信息 -->
          <div v-if="getStatus(acc.id).error" class="card-error">
            <div class="error-header">
              <span class="error-label">{{ t('access.error') }}</span>
              <button class="btn-icon btn-sm" @click="copyText(getStatus(acc.id).error)" :title="t('common.copy')">
                <svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
                  <rect x="9" y="9" width="13" height="13" rx="2" ry="2"></rect>
                  <path d="M5 15H4a2 2 0 01-2-2V4a2 2 0 012-2h9a2 2 0 012 2v1"></path>
                </svg>
              </button>
            </div>
            <pre class="error-msg">{{ getStatus(acc.id).error }}</pre>
          </div>
          <!-- 最新日志 + 查看日志 -->
          <div v-if="getStatus(acc.id).running || getStatus(acc.id).error" class="card-log-bar">
            <span class="last-log" v-if="getStatus(acc.id).lastLog">{{ getStatus(acc.id).lastLog }}</span>
            <button class="btn-log" @click="openLogViewer(acc)">{{ t('access.viewLogs') }}</button>
          </div>
        </div>
      </div>
    </div>

    <!-- 创建/编辑弹窗 -->
    <div v-if="showForm" class="modal-overlay" @click.self="showForm = false">
      <div class="modal">
        <h3>{{ editingAccess ? t('access.edit') : t('access.create') }}</h3>

        <div class="form-group">
          <label>{{ t('access.name') }}</label>
          <input v-model="formData.name" :placeholder="t('access.namePlaceholder')" />
        </div>

        <div class="form-group">
          <label>{{ t('access.hostname') }}</label>
          <input v-model="formData.hostname" :placeholder="t('access.hostnamePlaceholder')" />
          <span class="field-hint">{{ t('access.hostnameHint') }}</span>
        </div>

        <div class="form-group">
          <label>{{ t('access.localPort') }}</label>
          <input v-model.number="formData.localPort" type="number" :placeholder="t('access.localPortPlaceholder')" />
          <span class="field-hint">{{ t('access.localPortHint') }}</span>
        </div>

        <!-- Access 认证（可选） -->
        <div class="auth-section">
          <div class="auth-section-title">{{ t('access.authSection') }}</div>
          <div class="form-group">
            <label>{{ t('access.serviceTokenId') }}</label>
            <input v-model="formData.serviceTokenId" placeholder="xxxxxxxx-xxxx-xxxx-xxxx-xxxxxxxxxxxx" />
          </div>
          <div class="form-group">
            <label>{{ t('access.serviceTokenSecret') }}</label>
            <input v-model="formData.serviceTokenSecret" type="password" placeholder="••••••••••••••••••••" />
          </div>
          <span class="field-hint">{{ t('access.serviceTokenHint') }}</span>
        </div>

        <div class="form-group auto-start-row">
          <label class="switch-label">
            <span>{{ t('access.autoStart') }}</span>
            <label class="switch">
              <input type="checkbox" v-model="formData.autoStart" />
              <span class="slider"></span>
            </label>
          </label>
        </div>

        <div class="modal-actions">
          <button class="btn-secondary" @click="showForm = false">{{ t('common.cancel') }}</button>
          <button class="btn-primary" @click="saveForm">{{ t('common.save') }}</button>
        </div>
      </div>
    </div>

    <!-- 日志查看器弹窗 -->
    <div v-if="showLogs" class="modal-overlay">
      <div class="modal modal-logs">
        <div class="log-header">
          <h3>{{ logAccessName }} - {{ t('access.logs') }}</h3>
          <div class="log-actions">
            <button class="btn-secondary btn-sm" @click="copyAllLogs">{{ t('common.copy') }}</button>
            <button class="btn-secondary btn-sm" @click="closeLogViewer">{{ t('common.close') }}</button>
          </div>
        </div>
        <div class="log-content">
          <div v-if="logLines.length === 0" class="log-empty">{{ t('access.noLogs') }}</div>
          <div v-for="(line, i) in logLines" :key="i" class="log-line">{{ line }}</div>
        </div>
      </div>
    </div>
  </div>
</template>

<style scoped>
.access-view {
  max-width: 800px;
  margin: 0 auto;
}

.toolbar {
  display: flex;
  align-items: center;
  justify-content: space-between;
  margin-bottom: 16px;
}
.toolbar h2 {
  font-size: 20px;
  font-weight: 600;
}

/* 使用说明 */
.guide-box {
  background: rgba(0,113,227,0.05);
  border: 1px solid rgba(0,113,227,0.15);
  border-radius: var(--radius);
  padding: 12px 16px;
  font-size: 13px;
  color: var(--text-secondary);
  line-height: 1.6;
  margin-bottom: 20px;
}
.guide-title {
  margin: 0 0 6px 0;
  font-weight: 500;
  color: var(--text-primary);
}
.guide-box ol {
  margin: 0;
  padding-left: 20px;
}
.guide-box li {
  margin-bottom: 2px;
}

.notice {
  background: rgba(243,139,168,0.1);
  color: var(--danger);
  padding: 12px 16px;
  border-radius: var(--radius);
  margin-bottom: 16px;
  font-size: 14px;
}

.empty {
  text-align: center;
  color: var(--text-muted);
  padding: 60px 20px;
  font-size: 15px;
}

.cards {
  display: flex;
  flex-direction: column;
  gap: 12px;
}

.card {
  background: var(--bg-secondary);
  border: 1px solid var(--border);
  border-radius: 10px;
  padding: 16px;
  transition: border-color 0.2s;
}
.card:hover {
  border-color: var(--accent);
}

.card-header {
  display: flex;
  align-items: center;
  justify-content: space-between;
  margin-bottom: 10px;
}
.card-title {
  display: flex;
  align-items: center;
  gap: 8px;
}
.card-name {
  font-size: 16px;
  font-weight: 600;
}
.card-actions {
  display: flex;
  gap: 6px;
}
.card-body {
  font-size: 13px;
}

/* 路由信息 */
.card-route {
  display: flex;
  align-items: center;
  gap: 8px;
  color: var(--text-secondary);
  font-family: 'SF Mono', Monaco, monospace;
  font-size: 13px;
  margin-bottom: 8px;
}
.route-hostname {
  color: var(--accent);
}
.route-arrow {
  color: var(--text-muted);
}
.route-local {
  color: var(--text-primary);
}

/* 使用方式 */
.card-usage {
  display: flex;
  align-items: center;
  gap: 8px;
  margin-bottom: 6px;
}
.usage-label {
  color: var(--text-muted);
  flex-shrink: 0;
}
.usage-cmd {
  flex: 1;
  font-family: 'SF Mono', Monaco, monospace;
  font-size: 12px;
  color: var(--text-primary);
  background: var(--bg-surface);
  padding: 3px 8px;
  border-radius: 4px;
}

/* 错误信息 */
.card-error {
  margin-top: 8px;
  background: rgba(255,59,48,0.06);
  border: 1px solid rgba(255,59,48,0.15);
  border-radius: var(--radius);
  padding: 8px 12px;
}
.error-header {
  display: flex;
  align-items: center;
  justify-content: space-between;
}
.error-label {
  font-size: 12px;
  font-weight: 500;
  color: var(--danger);
}
.error-msg {
  margin: 4px 0 0 0;
  font-size: 12px;
  color: var(--text-secondary);
  font-family: 'SF Mono', Monaco, monospace;
  white-space: pre-wrap;
  word-break: break-all;
}

/* 日志栏 */
.card-log-bar {
  margin-top: 8px;
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 8px;
}
.last-log {
  flex: 1;
  font-size: 11px;
  color: var(--text-muted);
  font-family: 'SF Mono', Monaco, monospace;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.btn-log {
  flex-shrink: 0;
  background: none;
  border: 1px solid var(--border);
  border-radius: var(--radius);
  color: var(--text-secondary);
  font-size: 11px;
  padding: 2px 8px;
  cursor: pointer;
  transition: all 0.2s;
}
.btn-log:hover {
  border-color: var(--accent);
  color: var(--accent);
}

/* badge */
.badge-auto {
  background: rgba(52,199,89,0.1);
  color: #34c759;
  font-size: 11px;
}

/* 弹窗 */
.field-hint {
  display: block;
  font-size: 12px;
  color: var(--text-muted);
  margin-top: 4px;
}

/* Access 认证区域 */
.auth-section {
  background: var(--bg-surface);
  border-radius: var(--radius);
  border: 1px solid var(--border);
  padding: 12px 14px;
  margin-bottom: 12px;
}
.auth-section-title {
  font-size: 12px;
  font-weight: 600;
  color: var(--text-muted);
  text-transform: uppercase;
  letter-spacing: 0.5px;
  margin-bottom: 10px;
}
.auth-section .form-group {
  margin-bottom: 10px;
}

/* 自启动开关 */
.auto-start-row {
  margin-top: 4px;
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
.switch input { opacity: 0; width: 0; height: 0; }
.slider {
  position: absolute;
  cursor: pointer;
  top: 0; left: 0; right: 0; bottom: 0;
  background: var(--border);
  border-radius: 24px;
  transition: 0.2s;
}
.slider::before {
  content: '';
  position: absolute;
  height: 18px; width: 18px;
  left: 3px; bottom: 3px;
  background: white;
  border-radius: 50%;
  transition: 0.2s;
}
.switch input:checked + .slider { background: var(--accent); }
.switch input:checked + .slider::before { transform: translateX(20px); }

/* 日志查看器 */
.modal-logs {
  width: 700px;
  max-width: 90vw;
  max-height: 80vh;
  display: flex;
  flex-direction: column;
}
.log-header {
  display: flex;
  align-items: center;
  justify-content: space-between;
  margin-bottom: 12px;
}
.log-header h3 {
  font-size: 15px;
  font-weight: 600;
}
.log-actions {
  display: flex;
  gap: 6px;
}
.log-content {
  flex: 1;
  overflow-y: auto;
  background: #1a1a2e;
  border-radius: var(--radius);
  padding: 12px;
  max-height: 400px;
  min-height: 200px;
}
.log-line {
  font-size: 11px;
  font-family: 'SF Mono', Monaco, monospace;
  color: #e0e0e0;
  line-height: 1.5;
  word-break: break-all;
}
.log-empty {
  color: #666;
  font-size: 13px;
  text-align: center;
  padding: 40px;
}
</style>
