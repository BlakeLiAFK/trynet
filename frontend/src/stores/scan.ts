import { reactive } from 'vue'

export interface ScanResult {
  ip: string
  port: number
  proto: string
  latency: number
}

// 全局单例，整个 app 生命周期内缓存
export const scanStore = reactive({
  results: [] as ScanResult[],
  scanning: false,
  progress: 0,
  total: 0,
  subnetBits: 24,
  done: false, // 至少扫描过一次
})
