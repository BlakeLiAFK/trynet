<script setup lang="ts">
import { ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { scanStore, type ScanResult } from '../stores/scan'

const { t } = useI18n()

// 添加隧道弹窗
const showAdd = ref(false)
const addTarget = ref<ScanResult | null>(null)
const addForm = ref({ name: '', tunnelType: 'quick', token: '', customDomain: '', autoStart: false, noTLSVerify: false })
const addLoading = ref(false)

async function startScan() {
  if (scanStore.scanning) return
  const { EventsOn } = await import('../../wailsjs/runtime/runtime')
  scanStore.scanning = true
  scanStore.progress = 0
  scanStore.total = 0
  scanStore.results = []
  scanStore.done = false
  EventsOn('scan-progress', (scanned: number, total: number) => {
    scanStore.progress = scanned
    scanStore.total = total
  })
  try {
    const App = await import('../../wailsjs/go/main/App')
    scanStore.results = await App.ScanLAN(scanStore.subnetBits)
    scanStore.done = true
  } finally {
    scanStore.scanning = false
  }
}

async function openInBrowser(r: ScanResult) {
  const url = `${r.proto}://${r.ip}:${r.port}`
  try {
    const App = await import('../../wailsjs/go/main/App')
    await App.OpenURL(url)
  } catch {
    window.open(url, '_blank')
  }
}

function openAdd(r: ScanResult) {
  addTarget.value = r
  addForm.value = {
    name: `${r.ip}:${r.port}`,
    tunnelType: 'quick',
    token: '',
    customDomain: '',
    autoStart: false,
    noTLSVerify: r.proto === 'https',
  }
  showAdd.value = true
}

async function saveAdd() {
  if (!addTarget.value) return
  addLoading.value = true
  try {
    const App = await import('../../wailsjs/go/main/App')
    const r = addTarget.value
    const { name, tunnelType, token, customDomain, autoStart, noTLSVerify } = addForm.value
    await App.CreateTunnel(name, r.ip, r.port, r.proto, tunnelType, token, customDomain, autoStart, noTLSVerify)
    showAdd.value = false
  } finally {
    addLoading.value = false
  }
}

function progressPct() {
  if (scanStore.total === 0) return 0
  return Math.round(scanStore.progress / scanStore.total * 100)
}
</script>

<template>
  <div class="scan-view">
    <!-- 工具栏 -->
    <div class="toolbar">
      <h2>{{ t('nav.scan') }}</h2>
      <div class="toolbar-right">
        <div class="subnet-btns">
          <button
            v-for="bits in [24, 16]"
            :key="bits"
            type="button"
            :class="['btn-secondary', 'btn-sm', { active: scanStore.subnetBits === bits }]"
            :disabled="scanStore.scanning"
            @click="scanStore.subnetBits = bits"
          >/{{ bits }}</button>
        </div>
        <button
          class="btn-primary"
          :disabled="scanStore.scanning"
          @click="startScan"
        >
          {{ scanStore.scanning ? t('tunnel.scanning') : (scanStore.done ? t('scan.refresh') : t('scan.start')) }}
        </button>
      </div>
    </div>

    <!-- 进度条 -->
    <div v-if="scanStore.scanning" class="progress-wrap">
      <div class="progress-bar" :style="{ width: progressPct() + '%' }"></div>
      <span class="progress-tip">{{ scanStore.progress }} / {{ scanStore.total }} &nbsp;({{ progressPct() }}%)</span>
    </div>

    <!-- 空态 -->
    <div v-else-if="!scanStore.done" class="empty">{{ t('scan.hint') }}</div>
    <div v-else-if="scanStore.results.length === 0" class="empty">{{ t('tunnel.scanEmpty') }}</div>

    <!-- 结果列表 -->
    <div v-else class="result-list">
      <div class="result-header">
        <span>{{ t('scan.foundCount', { n: scanStore.results.length }) }}</span>
      </div>
      <div v-for="r in scanStore.results" :key="r.ip + r.port" class="result-row">
        <span :class="['badge', r.proto === 'https' ? 'badge-success' : 'badge-muted']">
          {{ r.proto.toUpperCase() }}
        </span>
        <span class="result-addr">{{ r.ip }}:{{ r.port }}</span>
        <span class="result-latency">{{ r.latency }}ms</span>
        <div class="result-actions">
          <button class="btn-secondary btn-sm" @click="openInBrowser(r)">{{ t('scan.open') }}</button>
          <button class="btn-primary btn-sm" @click="openAdd(r)">+ {{ t('tunnel.create') }}</button>
        </div>
      </div>
    </div>

    <!-- 添加隧道弹窗 -->
    <div v-if="showAdd" class="modal-overlay" @click.self="showAdd = false">
      <div class="modal">
        <h3>{{ t('tunnel.create') }}</h3>

        <!-- 预填信息展示 -->
        <div class="prefill-info">
          <span :class="['badge', addTarget!.proto === 'https' ? 'badge-success' : 'badge-muted']">
            {{ addTarget!.proto.toUpperCase() }}
          </span>
          <span class="prefill-addr">{{ addTarget!.ip }}:{{ addTarget!.port }}</span>
          <span class="prefill-latency">{{ addTarget!.latency }}ms</span>
        </div>

        <div class="form-group">
          <label>{{ t('tunnel.name') }}</label>
          <input v-model="addForm.name" :placeholder="t('tunnel.namePlaceholder')" />
        </div>
        <div class="form-group">
          <label>{{ t('tunnel.type') }}</label>
          <div class="type-switcher">
            <button
              :class="['btn-secondary', { active: addForm.tunnelType === 'quick' }]"
              type="button"
              @click="addForm.tunnelType = 'quick'"
            >Quick Tunnel</button>
            <button
              :class="['btn-secondary', { active: addForm.tunnelType === 'named' }]"
              type="button"
              @click="addForm.tunnelType = 'named'"
            >Named Tunnel</button>
          </div>
        </div>
        <template v-if="addForm.tunnelType === 'named'">
          <div class="form-group">
            <label>{{ t('tunnel.token') }}</label>
            <textarea v-model="addForm.token" rows="3" :placeholder="t('tunnel.tokenPlaceholder')"></textarea>
          </div>
          <div class="form-group">
            <label>{{ t('tunnel.customDomain') }}</label>
            <input v-model="addForm.customDomain" :placeholder="t('tunnel.domainPlaceholder')" />
          </div>
        </template>
        <div v-if="addTarget!.proto === 'https' || addForm.tunnelType === 'named'" class="form-group auto-start-row">
          <label class="switch-label">
            <span>{{ t('tunnel.noTLSVerify') }}</span>
            <label class="switch">
              <input type="checkbox" v-model="addForm.noTLSVerify" />
              <span class="slider"></span>
            </label>
          </label>
        </div>
        <div class="form-group auto-start-row">
          <label class="switch-label">
            <span>{{ t('tunnel.autoStart') }}</span>
            <label class="switch">
              <input type="checkbox" v-model="addForm.autoStart" />
              <span class="slider"></span>
            </label>
          </label>
        </div>
        <div class="modal-actions">
          <button class="btn-secondary" @click="showAdd = false">{{ t('common.cancel') }}</button>
          <button class="btn-primary" :disabled="addLoading" @click="saveAdd">{{ t('common.save') }}</button>
        </div>
      </div>
    </div>
  </div>
</template>

<style scoped>
.scan-view {
  max-width: 800px;
  margin: 0 auto;
}

.toolbar {
  display: flex;
  align-items: center;
  justify-content: space-between;
  margin-bottom: 20px;
}
.toolbar h2 {
  font-size: 20px;
  font-weight: 600;
}
.toolbar-right {
  display: flex;
  align-items: center;
  gap: 8px;
}
.subnet-btns {
  display: flex;
  gap: 4px;
}
.subnet-btns .active {
  background: var(--accent);
  color: #fff;
  border-color: var(--accent);
}

.progress-wrap {
  margin-bottom: 16px;
  display: flex;
  flex-direction: column;
  gap: 6px;
}
.progress-bar-bg {
  width: 100%;
  height: 4px;
  background: var(--border);
  border-radius: 2px;
  overflow: hidden;
}
.progress-bar {
  height: 4px;
  background: var(--accent);
  border-radius: 2px;
  transition: width 0.2s;
}
.progress-tip {
  font-size: 12px;
  color: var(--text-muted);
}

.empty {
  text-align: center;
  color: var(--text-muted);
  padding: 60px 20px;
  font-size: 15px;
}

.result-list {
  display: flex;
  flex-direction: column;
  gap: 6px;
}
.result-header {
  font-size: 13px;
  color: var(--text-muted);
  margin-bottom: 4px;
}
.result-row {
  display: flex;
  align-items: center;
  gap: 10px;
  padding: 10px 14px;
  background: var(--bg-secondary);
  border: 1px solid var(--border);
  border-radius: 8px;
  transition: border-color 0.2s;
}
.result-row:hover {
  border-color: var(--accent);
}
.result-addr {
  flex: 1;
  font-family: 'SF Mono', Monaco, monospace;
  font-size: 13px;
  color: var(--text-primary);
}
.result-latency {
  font-size: 12px;
  color: var(--text-muted);
  font-family: 'SF Mono', Monaco, monospace;
  width: 50px;
  text-align: right;
}
.result-actions {
  display: flex;
  gap: 6px;
}

/* 预填信息 */
.prefill-info {
  display: flex;
  align-items: center;
  gap: 8px;
  padding: 8px 12px;
  background: var(--bg-surface);
  border-radius: var(--radius);
  margin-bottom: 14px;
  font-size: 13px;
}
.prefill-addr {
  flex: 1;
  font-family: 'SF Mono', Monaco, monospace;
  color: var(--text-primary);
}
.prefill-latency {
  color: var(--text-muted);
  font-family: 'SF Mono', Monaco, monospace;
}

/* 复用 TunnelList 的 switch 样式 */
.auto-start-row { margin-top: 4px; }
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

.type-switcher { display: flex; gap: 6px; }
.type-switcher .active {
  background: var(--accent);
  color: #fff;
  font-weight: 600;
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
textarea:focus { border-color: var(--accent); }
</style>
