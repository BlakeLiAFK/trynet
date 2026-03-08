<script setup lang="ts">
import { ref, computed, onMounted, onUnmounted, nextTick } from 'vue'
import { useI18n } from 'vue-i18n'

const { t } = useI18n()

interface Tunnel {
  id: number
  name: string
  localHost: string
  localPort: number
  protocol: string
  tunnelType: string
  token: string
  customDomain: string
  autoStart: boolean
  noTLSVerify: boolean
  createdAt: string
  updatedAt: string
}

interface TunnelStatus {
  running: boolean
  url: string
  error: string
  lastLog: string
}

interface TunnelMetrics {
  haConnections: number
  totalRequests: number
  requestErrors: number
  latestRtt: number
  sentBytes: number
  receivedBytes: number
  concurrentRequests: number
}

const tunnels = ref<Tunnel[]>([])
const statuses = ref<Record<number, TunnelStatus>>({})
const metrics = ref<Record<number, TunnelMetrics>>({})
const showForm = ref(false)
const editingTunnel = ref<Tunnel | null>(null)
const cfdInstalled = ref(false)
const loading = ref(true)


// 搜索和排序
const searchQuery = ref('')
const sortMode = ref<'default' | 'name' | 'status'>('default')

// 日志查看器
const showLogs = ref(false)
const logTunnelId = ref(0)
const logTunnelName = ref('')
const logLines = ref<string[]>([])
let logPollTimer: number | null = null

// 统计详情弹窗
const showMetricsDetail = ref(false)
const metricsTunnelId = ref(0)
const metricsTunnelName = ref('')

let pollTimer: number | null = null

const formData = ref({
  name: '',
  localHost: '127.0.0.1',
  localPort: 8080,
  protocol: 'http',
  tunnelType: 'quick',
  token: '',
  customDomain: '',
  autoStart: false,
  noTLSVerify: false,
})

// 过滤和排序后的隧道列表
const filteredTunnels = computed(() => {
  let list = [...tunnels.value]

  // 搜索过滤
  if (searchQuery.value.trim()) {
    const q = searchQuery.value.toLowerCase()
    list = list.filter(t =>
      t.name.toLowerCase().includes(q) ||
      t.localHost.includes(q) ||
      String(t.localPort).includes(q) ||
      (t.customDomain && t.customDomain.toLowerCase().includes(q))
    )
  }

  // 排序
  if (sortMode.value === 'name') {
    list.sort((a, b) => a.name.localeCompare(b.name))
  } else if (sortMode.value === 'status') {
    list.sort((a, b) => {
      const aRunning = getStatus(a.id).running ? 0 : 1
      const bRunning = getStatus(b.id).running ? 0 : 1
      return aRunning - bRunning
    })
  }

  return list
})

async function loadData() {
  try {
    const App = await import('../../wailsjs/go/main/App')
    cfdInstalled.value = await App.IsCloudflaredInstalled()
    tunnels.value = await App.GetTunnels()
    statuses.value = await App.GetAllStatuses()
  } catch (e) {
    console.error(e)
  } finally {
    loading.value = false
  }
}

async function pollStatuses() {
  try {
    const App = await import('../../wailsjs/go/main/App')
    statuses.value = await App.GetAllStatuses()
    // 轮询运行中隧道的 metrics
    await pollMetrics()
  } catch {}
}

async function pollMetrics() {
  const App = await import('../../wailsjs/go/main/App')
  for (const tun of tunnels.value) {
    if (getStatus(tun.id).running) {
      try {
        const m = await App.GetTunnelMetrics(tun.id)
        if (m) {
          metrics.value[tun.id] = m
        }
      } catch {}
    } else {
      delete metrics.value[tun.id]
    }
  }
}

function openCreate() {
  editingTunnel.value = null
  formData.value = { name: '', localHost: '127.0.0.1', localPort: 8080, protocol: 'http', tunnelType: 'quick', token: '', customDomain: '', autoStart: false, noTLSVerify: false }
  showForm.value = true
}

function openEdit(t: Tunnel) {
  editingTunnel.value = t
  formData.value = {
    name: t.name,
    localHost: t.localHost,
    localPort: t.localPort,
    protocol: t.protocol,
    tunnelType: t.tunnelType || 'quick',
    token: t.token || '',
    customDomain: t.customDomain || '',
    autoStart: t.autoStart || false,
    noTLSVerify: t.noTLSVerify || false,
  }
  showForm.value = true
}

async function saveForm() {
  const App = await import('../../wailsjs/go/main/App')
  const { name, localHost, localPort, protocol, tunnelType, token, customDomain, autoStart, noTLSVerify } = formData.value
  if (editingTunnel.value) {
    await App.UpdateTunnel(editingTunnel.value.id, name, localHost, localPort, protocol, tunnelType, token, customDomain, autoStart, noTLSVerify)
  } else {
    await App.CreateTunnel(name, localHost, localPort, protocol, tunnelType, token, customDomain, autoStart, noTLSVerify)
  }
  showForm.value = false
  await loadData()
}

async function deleteTunnel(id: number) {
  if (!confirm(t('tunnel.confirmDelete'))) return
  const App = await import('../../wailsjs/go/main/App')
  await App.DeleteTunnel(id)
  await loadData()
}

async function startTunnel(id: number) {
  const App = await import('../../wailsjs/go/main/App')
  await App.StartTunnel(id)
  setTimeout(pollStatuses, 500)
}

async function stopTunnel(id: number) {
  const App = await import('../../wailsjs/go/main/App')
  await App.StopTunnel(id)
  setTimeout(pollStatuses, 500)
}

function copyText(text: string) {
  navigator.clipboard.writeText(text)
}

async function openUrl(url: string) {
  try {
    const App = await import('../../wailsjs/go/main/App')
    await App.OpenURL(url)
  } catch {
    window.open(url, '_blank')
  }
}

function getStatus(id: number): TunnelStatus {
  return statuses.value[id] || { running: false, url: '', error: '', lastLog: '' }
}

function getPublicUrl(tun: Tunnel): string {
  if (tun.tunnelType === 'named' && tun.customDomain) {
    return 'https://' + tun.customDomain
  }
  const s = getStatus(tun.id)
  return s.url && s.url !== 'connected' ? s.url : ''
}

function getMetrics(id: number): TunnelMetrics | null {
  return metrics.value[id] || null
}

// 格式化字节数
function formatBytes(bytes: number): string {
  if (bytes === 0) return '0 B'
  const units = ['B', 'KB', 'MB', 'GB', 'TB']
  const i = Math.floor(Math.log(bytes) / Math.log(1024))
  return (bytes / Math.pow(1024, i)).toFixed(i > 0 ? 1 : 0) + ' ' + units[i]
}

// 统计详情弹窗
function openMetricsDetail(tun: Tunnel) {
  metricsTunnelId.value = tun.id
  metricsTunnelName.value = tun.name
  showMetricsDetail.value = true
}

// 日志查看器
async function openLogViewer(tun: Tunnel) {
  logTunnelId.value = tun.id
  logTunnelName.value = tun.name
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

// 判断是否在底部附近（容差 30px）
function isScrolledToBottom(el: Element): boolean {
  return el.scrollHeight - el.scrollTop - el.clientHeight < 30
}

async function fetchLogs() {
  try {
    const App = await import('../../wailsjs/go/main/App')
    const el = document.querySelector('.log-content')
    const wasAtBottom = !el || isScrolledToBottom(el)
    logLines.value = await App.GetTunnelLogs(logTunnelId.value) || []
    if (wasAtBottom) {
      await nextTick()
      if (el) el.scrollTop = el.scrollHeight
    }
  } catch {}
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
  <div class="tunnel-list">
    <div class="toolbar">
      <h2>{{ t('nav.tunnels') }}</h2>
      <div class="toolbar-right">
        <div class="search-box">
          <svg class="search-icon" width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
            <circle cx="11" cy="11" r="8"></circle>
            <path d="M21 21l-4.35-4.35"></path>
          </svg>
          <input
            v-model="searchQuery"
            :placeholder="t('tunnel.search')"
            class="search-input"
          />
        </div>
        <select v-model="sortMode" class="sort-select">
          <option value="default">{{ t('tunnel.sortDefault') }}</option>
          <option value="name">{{ t('tunnel.sortName') }}</option>
          <option value="status">{{ t('tunnel.sortStatus') }}</option>
        </select>
        <button class="btn-primary" @click="openCreate" :disabled="!cfdInstalled">
          + {{ t('tunnel.create') }}
        </button>
      </div>
    </div>

    <div v-if="!cfdInstalled && !loading" class="notice">
      {{ t('cfd.notInstalled') }} - {{ t('nav.settings') }}
    </div>

    <div v-if="loading" class="empty">Loading...</div>

    <div v-else-if="tunnels.length === 0" class="empty">
      {{ t('tunnel.noTunnels') }}
    </div>

    <div v-else-if="filteredTunnels.length === 0" class="empty">
      No results
    </div>

    <div v-else class="cards">
      <div v-for="tun in filteredTunnels" :key="tun.id" class="card">
        <div class="card-header">
          <div class="card-title">
            <span class="card-name">{{ tun.name }}</span>
            <span :class="['badge', getStatus(tun.id).running ? 'badge-success' : 'badge-muted']">
              {{ getStatus(tun.id).running ? t('tunnel.running') : t('tunnel.stopped') }}
            </span>
            <span v-if="tun.autoStart" class="badge badge-auto">{{ t('tunnel.autoStart') }}</span>
          </div>
          <div class="card-actions">
            <button
              v-if="!getStatus(tun.id).running"
              class="btn-success btn-sm"
              @click="startTunnel(tun.id)"
              :disabled="!cfdInstalled"
            >{{ t('tunnel.start') }}</button>
            <button
              v-else
              class="btn-danger btn-sm"
              @click="stopTunnel(tun.id)"
            >{{ t('tunnel.stop') }}</button>
            <button class="btn-secondary btn-sm" @click="openEdit(tun)">{{ t('tunnel.edit') }}</button>
            <button class="btn-danger btn-sm" @click="deleteTunnel(tun.id)">{{ t('tunnel.delete') }}</button>
          </div>
        </div>
        <div class="card-body">
          <div class="card-info">
            <span class="badge badge-type">{{ tun.tunnelType === 'named' ? 'Named' : 'Quick' }}</span>
            <template v-if="tun.tunnelType === 'named'">
              <span class="label" style="margin-left:8px">{{ t('tunnel.customDomain') }}:</span>
              <span class="value">{{ tun.customDomain || '-' }}</span>
            </template>
            <template v-else>
              <span class="label" style="margin-left:8px">{{ t('tunnel.localHost') }}:</span>
              <span class="value">{{ tun.localHost }}:{{ tun.localPort }}</span>
            </template>
          </div>
          <!-- 公网地址 -->
          <div v-if="getStatus(tun.id).running && getPublicUrl(tun)" class="card-url">
            <span class="label">{{ t('tunnel.url') }}:</span>
            <a class="url-link" href="#" @click.prevent="openUrl(getPublicUrl(tun))">
              {{ getPublicUrl(tun) }}
            </a>
            <button class="btn-icon btn-sm" @click="copyText(getPublicUrl(tun))" :title="t('common.copy')">
              <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
                <rect x="9" y="9" width="13" height="13" rx="2" ry="2"></rect>
                <path d="M5 15H4a2 2 0 01-2-2V4a2 2 0 012-2h9a2 2 0 012 2v1"></path>
              </svg>
            </button>
          </div>
          <div v-else-if="getStatus(tun.id).running" class="card-url">
            <span class="waiting">{{ t('tunnel.waiting') }}</span>
          </div>
          <!-- 流量统计条 -->
          <div v-if="getStatus(tun.id).running && getMetrics(tun.id)" class="card-metrics" @click="openMetricsDetail(tun)">
            <span class="metric-item">
              <span class="metric-label">{{ t('tunnel.connections') }}</span>
              <span class="metric-value">{{ getMetrics(tun.id)!.haConnections }}</span>
            </span>
            <span class="metric-item">
              <span class="metric-label">{{ t('tunnel.requests') }}</span>
              <span class="metric-value">{{ getMetrics(tun.id)!.totalRequests }}</span>
            </span>
            <span class="metric-item" v-if="getMetrics(tun.id)!.requestErrors > 0">
              <span class="metric-label metric-error">{{ t('tunnel.errors') }}</span>
              <span class="metric-value metric-error">{{ getMetrics(tun.id)!.requestErrors }}</span>
            </span>
            <span class="metric-item" v-if="getMetrics(tun.id)!.latestRtt > 0">
              <span class="metric-label">{{ t('tunnel.rtt') }}</span>
              <span class="metric-value">{{ getMetrics(tun.id)!.latestRtt.toFixed(1) }}ms</span>
            </span>
            <span class="metric-item">
              <span class="metric-label">{{ t('tunnel.sent') }}</span>
              <span class="metric-value">{{ formatBytes(getMetrics(tun.id)!.sentBytes) }}</span>
            </span>
            <span class="metric-item">
              <span class="metric-label">{{ t('tunnel.received') }}</span>
              <span class="metric-value">{{ formatBytes(getMetrics(tun.id)!.receivedBytes) }}</span>
            </span>
          </div>
          <!-- 错误信息 -->
          <div v-if="getStatus(tun.id).error" class="card-error">
            <div class="error-header">
              <span class="error-label">{{ t('tunnel.error') }}</span>
              <button class="btn-icon btn-sm" @click="copyText(getStatus(tun.id).error)" :title="t('common.copy')">
                <svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
                  <rect x="9" y="9" width="13" height="13" rx="2" ry="2"></rect>
                  <path d="M5 15H4a2 2 0 01-2-2V4a2 2 0 012-2h9a2 2 0 012 2v1"></path>
                </svg>
              </button>
            </div>
            <pre class="error-msg">{{ getStatus(tun.id).error }}</pre>
          </div>
          <!-- 最新日志 + 查看日志按钮 -->
          <div v-if="getStatus(tun.id).running || getStatus(tun.id).error" class="card-log-bar">
            <span class="last-log" v-if="getStatus(tun.id).lastLog">{{ getStatus(tun.id).lastLog }}</span>
            <button class="btn-log" @click="openLogViewer(tun)">{{ t('tunnel.viewLogs') }}</button>
          </div>
        </div>
      </div>
    </div>

    <!-- 创建/编辑弹窗 -->
    <div v-if="showForm" class="modal-overlay">
      <div class="modal">
        <h3>{{ editingTunnel ? t('tunnel.edit') : t('tunnel.create') }}</h3>
        <div class="form-group">
          <label>{{ t('tunnel.name') }}</label>
          <input v-model="formData.name" :placeholder="t('tunnel.namePlaceholder')" />
        </div>
        <div class="form-group">
          <label>{{ t('tunnel.type') }}</label>
          <div class="type-switcher">
            <button
              :class="['btn-secondary', { active: formData.tunnelType === 'quick' }]"
              @click="formData.tunnelType = 'quick'; if (formData.protocol === 'tcp') formData.protocol = 'http'"
              type="button"
            >Quick Tunnel</button>
            <button
              :class="['btn-secondary', { active: formData.tunnelType === 'named' }]"
              @click="formData.tunnelType = 'named'"
              type="button"
            >Named Tunnel</button>
          </div>
        </div>
        <template v-if="formData.tunnelType === 'named'">
          <div class="named-guide">
            <p>{{ t('tunnel.namedGuide') }}</p>
            <ol>
              <li>{{ t('tunnel.namedStep1') }}</li>
              <li>{{ t('tunnel.namedStep2') }}</li>
              <li>{{ t('tunnel.namedStep3') }}</li>
            </ol>
          </div>
          <div class="form-group">
            <label>{{ t('tunnel.token') }}</label>
            <textarea v-model="formData.token" rows="3" :placeholder="t('tunnel.tokenPlaceholder')"></textarea>
          </div>
          <div class="form-group">
            <label>{{ t('tunnel.customDomain') }}</label>
            <input v-model="formData.customDomain" :placeholder="t('tunnel.domainPlaceholder')" />
          </div>
        </template>
        <div class="form-row">
          <div class="form-group">
            <label>{{ t('tunnel.localHost') }}</label>
            <input v-model="formData.localHost" :placeholder="t('tunnel.hostPlaceholder')" />
          </div>
          <div class="form-group">
            <label>{{ t('tunnel.localPort') }}</label>
            <input v-model.number="formData.localPort" type="number" :placeholder="t('tunnel.portPlaceholder')" />
          </div>
        </div>
        <div class="form-group">
          <label>{{ t('tunnel.protocol') }}</label>
          <select v-model="formData.protocol">
            <option value="http">HTTP</option>
            <option value="https">HTTPS</option>
            <option value="tcp" :disabled="formData.tunnelType === 'quick'">TCP {{ formData.tunnelType === 'quick' ? '(Named Tunnel only)' : '' }}</option>
          </select>
        </div>
        <div v-if="formData.protocol === 'https' || formData.tunnelType === 'named'" class="form-group auto-start-row">
          <label class="switch-label">
            <span>{{ t('tunnel.noTLSVerify') }}</span>
            <label class="switch">
              <input type="checkbox" v-model="formData.noTLSVerify" />
              <span class="slider"></span>
            </label>
          </label>
        </div>
        <div class="form-group auto-start-row">
          <label class="switch-label">
            <span>{{ t('tunnel.autoStart') }}</span>
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
          <h3>{{ logTunnelName }} - {{ t('tunnel.logs') }}</h3>
          <div class="log-actions">
            <button class="btn-secondary btn-sm" @click="copyAllLogs">{{ t('common.copy') }}</button>
            <button class="btn-secondary btn-sm" @click="closeLogViewer">{{ t('common.close') }}</button>
          </div>
        </div>
        <div class="log-content">
          <div v-if="logLines.length === 0" class="log-empty">{{ t('tunnel.noLogs') }}</div>
          <div v-for="(line, i) in logLines" :key="i" class="log-line">{{ line }}</div>
        </div>
      </div>
    </div>

    <!-- 统计详情弹窗 -->
    <div v-if="showMetricsDetail" class="modal-overlay" @click.self="showMetricsDetail = false">
      <div class="modal modal-metrics">
        <div class="log-header">
          <h3>{{ metricsTunnelName }} - {{ t('tunnel.metrics') }}</h3>
          <button class="btn-secondary btn-sm" @click="showMetricsDetail = false">{{ t('common.close') }}</button>
        </div>
        <div v-if="getMetrics(metricsTunnelId)" class="metrics-grid">
          <div class="metrics-card">
            <span class="metrics-card-label">{{ t('tunnel.connections') }}</span>
            <span class="metrics-card-value">{{ getMetrics(metricsTunnelId)!.haConnections }}</span>
          </div>
          <div class="metrics-card">
            <span class="metrics-card-label">{{ t('tunnel.requests') }}</span>
            <span class="metrics-card-value">{{ getMetrics(metricsTunnelId)!.totalRequests }}</span>
          </div>
          <div class="metrics-card">
            <span class="metrics-card-label">{{ t('tunnel.errors') }}</span>
            <span :class="['metrics-card-value', { 'metric-error': getMetrics(metricsTunnelId)!.requestErrors > 0 }]">
              {{ getMetrics(metricsTunnelId)!.requestErrors }}
            </span>
          </div>
          <div class="metrics-card">
            <span class="metrics-card-label">{{ t('tunnel.concurrent') }}</span>
            <span class="metrics-card-value">{{ getMetrics(metricsTunnelId)!.concurrentRequests }}</span>
          </div>
          <div class="metrics-card">
            <span class="metrics-card-label">{{ t('tunnel.rtt') }}</span>
            <span class="metrics-card-value">
              {{ getMetrics(metricsTunnelId)!.latestRtt > 0 ? getMetrics(metricsTunnelId)!.latestRtt.toFixed(1) + 'ms' : '-' }}
            </span>
          </div>
          <div class="metrics-card">
            <span class="metrics-card-label">{{ t('tunnel.sent') }}</span>
            <span class="metrics-card-value">{{ formatBytes(getMetrics(metricsTunnelId)!.sentBytes) }}</span>
          </div>
          <div class="metrics-card">
            <span class="metrics-card-label">{{ t('tunnel.received') }}</span>
            <span class="metrics-card-value">{{ formatBytes(getMetrics(metricsTunnelId)!.receivedBytes) }}</span>
          </div>
        </div>
        <div v-else class="metrics-empty">{{ t('tunnel.noMetrics') }}</div>
      </div>
    </div>
  </div>
</template>

<style scoped>
.tunnel-list {
  max-width: 800px;
  margin: 0 auto;
}

.toolbar {
  display: flex;
  align-items: center;
  justify-content: space-between;
  margin-bottom: 20px;
  gap: 12px;
}

.toolbar h2 {
  font-size: 20px;
  font-weight: 600;
  flex-shrink: 0;
}

.toolbar-right {
  display: flex;
  align-items: center;
  gap: 8px;
}

.search-box {
  position: relative;
  display: flex;
  align-items: center;
}
.search-icon {
  position: absolute;
  left: 10px;
  color: var(--text-muted);
  pointer-events: none;
}
.search-input {
  padding: 6px 10px 6px 30px !important;
  font-size: 13px !important;
  width: 180px;
  border-radius: 6px !important;
  background: var(--bg-surface);
  border: 1px solid var(--border);
  color: var(--text-primary);
  outline: none;
  transition: border-color 0.2s, width 0.2s;
}
.search-input:focus {
  border-color: var(--accent);
  width: 220px;
}
.search-input::placeholder {
  color: var(--text-muted);
}

.sort-select {
  padding: 6px 10px !important;
  font-size: 13px !important;
  border-radius: 6px !important;
  background: var(--bg-surface);
  border: 1px solid var(--border);
  color: var(--text-primary);
  cursor: pointer;
  width: auto;
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

.card-info {
  color: var(--text-secondary);
  display: flex;
  align-items: center;
  gap: 4px;
}

.label {
  color: var(--text-muted);
}

.value {
  color: var(--text-primary);
  font-family: 'SF Mono', Monaco, monospace;
}

.card-url {
  margin-top: 8px;
  display: flex;
  align-items: center;
  gap: 6px;
}

.url-link {
  color: var(--accent);
  text-decoration: none;
  font-family: 'SF Mono', Monaco, monospace;
  font-size: 13px;
  cursor: pointer;
}
.url-link:hover {
  text-decoration: underline;
}

.waiting {
  color: var(--text-muted);
  font-style: italic;
}

/* 流量统计条 */
.card-metrics {
  margin-top: 8px;
  display: flex;
  align-items: center;
  gap: 12px;
  padding: 8px 12px;
  background: var(--bg-surface);
  border-radius: 6px;
  cursor: pointer;
  transition: background 0.2s;
}
.card-metrics:hover {
  background: var(--bg-hover);
}
.metric-item {
  display: flex;
  align-items: center;
  gap: 4px;
}
.metric-label {
  font-size: 11px;
  color: var(--text-muted);
}
.metric-value {
  font-size: 12px;
  font-weight: 600;
  color: var(--text-primary);
  font-family: 'SF Mono', Monaco, monospace;
}
.metric-error {
  color: var(--danger) !important;
}

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

/* 统计详情弹窗 */
.modal-metrics {
  width: 480px;
  max-width: 90vw;
}
.metrics-grid {
  display: grid;
  grid-template-columns: repeat(3, 1fr);
  gap: 12px;
  margin-top: 4px;
}
.metrics-card {
  background: var(--bg-surface);
  border-radius: 8px;
  padding: 14px;
  display: flex;
  flex-direction: column;
  gap: 4px;
  text-align: center;
}
.metrics-card-label {
  font-size: 12px;
  color: var(--text-muted);
}
.metrics-card-value {
  font-size: 20px;
  font-weight: 700;
  color: var(--text-primary);
  font-family: 'SF Mono', Monaco, monospace;
}
.metrics-empty {
  text-align: center;
  color: var(--text-muted);
  padding: 30px;
  font-size: 14px;
}

.type-switcher {
  display: flex;
  gap: 6px;
}

.type-switcher .active {
  background: var(--accent);
  color: #ffffff;
  font-weight: 600;
}

.badge-type {
  background: rgba(0,113,227,0.08);
  color: var(--accent);
}

.badge-auto {
  background: rgba(52,199,89,0.1);
  color: #34c759;
  font-size: 11px;
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
.switch input {
  opacity: 0;
  width: 0;
  height: 0;
}
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

.named-guide {
  background: rgba(0,113,227,0.05);
  border: 1px solid rgba(0,113,227,0.15);
  border-radius: var(--radius);
  padding: 12px 16px;
  font-size: 13px;
  color: var(--text-secondary);
  line-height: 1.6;
}
.named-guide p {
  margin: 0 0 6px 0;
  font-weight: 500;
  color: var(--text-primary);
}
.named-guide ol {
  margin: 0;
  padding-left: 20px;
}
.named-guide li {
  margin-bottom: 2px;
}

textarea {
  background: var(--bg-primary);
  border: 1px solid var(--border);
  border-radius: var(--radius);
  color: var(--text-primary);
  padding: 10px 12px;
  font-size: 13px;
  font-family: 'SF Mono', Monaco, monospace;
  width: 100%;
  outline: none;
  resize: vertical;
  transition: border-color 0.2s;
}
textarea:focus {
  border-color: var(--accent);
}






</style>
