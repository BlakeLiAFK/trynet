# Cloudflare Tunnel 桌面管理工具

> 创建: 2026-02-08
> 状态: 已完成

## 目标

用 Go+Wails+Vue+TS+纯Go SQLite 实现一个 Cloudflare Tunnel 桌面管理工具，支持一键安装 cloudflared、傻瓜式配置、多隧道管理、中英文切换。

## 技术选型

- 后端: Go + Wails v2
- 前端: Vue 3 + TypeScript + Vite
- 数据库: modernc.org/sqlite (纯 Go, 无 CGO)
- 国际化: vue-i18n
- UI: 自定义轻量组件 (不引入重型 UI 库)

## 步骤

- [x] 1. Wails 项目初始化
- [x] 2. 后端核心模块
  - [x] 2.1 SQLite 数据库层 (隧道配置存储)
  - [x] 2.2 cloudflared 二进制管理 (下载/安装/版本检测)
  - [x] 2.3 隧道进程管理 (启动/停止/状态监控)
  - [x] 2.4 Wails 绑定 API
- [x] 3. 前端 UI
  - [x] 3.1 项目结构与 i18n 配置
  - [x] 3.2 主界面 (隧道列表)
  - [x] 3.3 创建/编辑隧道表单
  - [x] 3.4 状态显示与控制
  - [x] 3.5 设置页面 (语言切换等)
- [x] 4. 集成测试 (wails build 通过)
- [x] 5. 编译验证

## 完成标准

- [x] 可一键安装 cloudflared
- [x] 可创建/编辑/删除隧道配置
- [x] 可启动/停止隧道
- [x] 隧道运行后显示分配的 trycloudflare.com 域名
- [x] 支持中英文切换
- [x] 编译通过并可运行
