# 白色主题 + Named Tunnel + cloudflared 更新功能

> 创建: 2026-02-08
> 状态: 已完成

## 目标

将暗色主题切换为白色主题，新增 Named Tunnel 模式支持，新增 cloudflared 检查更新功能

## 步骤

- [x] 变更1: style.css - CSS 变量替换为白色主题，更新按钮/badge/scrollbar 颜色
- [x] 变更2: TunnelList.vue - 新增 tunnelType/token/customDomain 字段，表单双模式切换
- [x] 变更3: SettingsView.vue - 新增检查更新和更新按钮
- [x] 变更4: i18n zh.ts / en.ts - 新增国际化 key
- [x] 变更5: App.vue - 确认白色主题兼容（无需额外修改，CSS 变量自动生效）
- [x] 验证: TypeScript 检查通过 + Vite 构建通过 (352ms)

## 完成标准

- [x] 白色主题生效
- [x] Named Tunnel 表单可切换
- [x] cloudflared 更新按钮可用
- [x] TypeScript 无错误
- [x] Vite 构建通过
