# 命名隧道与自定义域名支持

> 创建: 2026-02-08
> 状态: 已完成

## 目标

后端支持命名隧道(named tunnel)和自定义域名，同时增加 cloudflared 更新检测功能。

## 步骤

- [x] 步骤 1: 修改 internal/db/db.go - Tunnel 结构体新增三个字段，migrate 添加 ALTER TABLE 兼容升级，更新 CRUD 方法
- [x] 步骤 2: 修改 internal/cfd/manager.go - 新增 GetLatestVersion 和 Update 方法
- [x] 步骤 3: 修改 internal/tunnel/manager.go - Start 方法支持 named 模式
- [x] 步骤 4: 修改 app.go - 更新 API 签名，新增 CheckCloudflaredUpdate 和 UpdateCloudflared
- [x] 步骤 5: 编译验证 go build ./... - 通过
- [x] 步骤 6: 代码检查 go vet ./... - 通过

## 完成标准

- [x] 编译通过
- [x] go vet 无报错
- [x] 所有新增字段在数据库层正确处理
- [x] 命名隧道和快速隧道两种模式都能正确构建命令
