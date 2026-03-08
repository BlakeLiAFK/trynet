<script setup lang="ts">
import { ref, onMounted } from 'vue'
import { useI18n } from 'vue-i18n'
import TunnelList from './components/TunnelList.vue'
import AccessView from './components/AccessView.vue'
import SettingsView from './components/SettingsView.vue'
import ScanView from './components/ScanView.vue'

const { t, locale } = useI18n()
const currentView = ref<'tunnels' | 'access' | 'scan' | 'settings'>('tunnels')

onMounted(async () => {
  try {
    const { GetSetting } = await import('../wailsjs/go/main/App')
    const lang = await GetSetting('language')
    if (lang) locale.value = lang
  } catch {}
})
</script>

<template>
  <div class="layout">
    <header class="header" style="--wails-draggable:drag">
      <div class="header-left">
        <h1 class="logo">TryNet</h1>
        <nav class="nav">
          <button
            :class="['nav-btn', { active: currentView === 'tunnels' }]"
            @click="currentView = 'tunnels'"
            style="--wails-draggable:no-drag"
          >{{ t('nav.tunnels') }}</button>
          <button
            :class="['nav-btn', { active: currentView === 'access' }]"
            @click="currentView = 'access'"
            style="--wails-draggable:no-drag"
          >{{ t('nav.access') }}</button>
          <button
            :class="['nav-btn', { active: currentView === 'scan' }]"
            @click="currentView = 'scan'"
            style="--wails-draggable:no-drag"
          >{{ t('nav.scan') }}</button>
          <button
            :class="['nav-btn', { active: currentView === 'settings' }]"
            @click="currentView = 'settings'"
            style="--wails-draggable:no-drag"
          >{{ t('nav.settings') }}</button>
        </nav>
      </div>
    </header>
    <main class="main">
      <TunnelList v-if="currentView === 'tunnels'" />
      <AccessView v-else-if="currentView === 'access'" />
      <ScanView v-else-if="currentView === 'scan'" />
      <SettingsView v-else />
    </main>
  </div>
</template>

<style scoped>
.layout {
  height: 100vh;
  display: flex;
  flex-direction: column;
}

.header {
  display: flex;
  align-items: center;
  justify-content: space-between;
  padding: 0 20px;
  height: 52px;
  background: var(--bg-secondary);
  border-bottom: 1px solid var(--border);
  flex-shrink: 0;
}

.header-left {
  display: flex;
  align-items: center;
  gap: 24px;
}

.logo {
  font-size: 18px;
  font-weight: 700;
  color: var(--accent);
  letter-spacing: -0.5px;
}

.nav {
  display: flex;
  gap: 4px;
}

.nav-btn {
  background: transparent;
  color: var(--text-secondary);
  font-size: 13px;
  padding: 6px 14px;
  border-radius: 6px;
  border: none;
  cursor: pointer;
  transition: all 0.15s;
}
.nav-btn:hover {
  color: var(--text-primary);
  background: var(--bg-surface);
}
.nav-btn.active {
  color: var(--text-primary);
  background: var(--bg-surface);
}

.main {
  flex: 1;
  overflow-y: auto;
  padding: 20px;
}
</style>
