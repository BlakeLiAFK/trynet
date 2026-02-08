# cloudflared Metrics 指标调研

> 创建: 2026-02-08
> 状态: 已完成

## 目标

调研 cloudflared --metrics 参数暴露的 Prometheus 指标体系，为监控集成提供技术方案。

## 步骤

- [x] 搜索 cloudflared 源码中的指标定义
- [x] 整理完整指标列表和含义
- [x] 调研 metrics 端口绑定和日志格式
- [x] 调研 Go 代码中解析 Prometheus 指标的方案
- [x] 输出调研报告

## 完成标准

- [x] 列出所有指标名称、类型、标签、含义
- [x] 提供日志端口解析方案
- [x] 提供 Go 代码指标解析方案
