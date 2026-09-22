# 计划索引

## 进行中

| 计划 | 说明 | 剩余 |
| --- | --- | --- |
| [plan_NamedTunnel.md](plan_NamedTunnel.md) | 固定域名模式：自动通过 Cloudflare 管理 API 创建/复用 named tunnel 和 DNS 记录，接入已有的连接逻辑 | 只差用真实 Cloudflare 账号做一次端到端验证 |

## 已归档

| 计划 | 说明 |
| --- | --- |
| [plan_MiniTunnel.md](archived/plan_MiniTunnel.md) | trynet 主体：cloudflared 快速隧道的迷你实现。HTTP/2 角色反转 + Cap'n Proto 注册、代理感知拨号、优雅退出、退避重连、启动横幅、图标嵌入、体积优化到 4.3 MB |
| [plan_PrintTokenInfoFix.md](archived/plan_PrintTokenInfoFix.md) | 修 `printTokenInfo` 自读环境变量导致 token 来源误判（flag 优先时会显示成环境变量） |
| [plan_cftest.md](archived/plan_cftest.md) | 补 `cfResolveZone` 的边界测试（完全相等、`notexample.com` 假阳性）和 `cfResolveAccount` 的错误提示 |
| [plan_Task7Followup.md](archived/plan_Task7Followup.md) | 修复"看起来在测、实际没测"的隧道复用测试，teardown 去掉多余等待，拆分超长测试文件 |
| [plan_QuickTunnelPersistence.md](archived/plan_QuickTunnelPersistence.md) | 快速隧道凭据存到当前目录的 `.trynet.json`（0600），重启优先复用、注册被拒才申请新的并覆盖存档；新增 `-new`/`-hidden` 参数；文件服务默认挡掉所有点开头路径防止凭据和 `.git`/`.env` 被下载 |
| [plan_FileService.md](archived/plan_FileService.md) | 把只读目录分享扩展成完整文件服务：目录列表 + 下载 + 上传，加 HTTP Basic 鉴权（默认开启，`-no-auth` 可关）；路径穿越用 `filepath.Rel` 判断包含关系、密码比较用常量时间比较、密码生成用 `crypto/rand`；上传同名不覆盖，加大小上限 |
| [plan_UICompose.md](archived/plan_UICompose.md) | 文件服务页面视觉构图重排：拖放区改成"空目录才显示的大盒子 + 页头 Upload 按钮 + 整页拖放覆盖层"，文件列表给承载面（卡片+分隔线），下载箭头静止态常驻可见，页头加面包屑；顺手补 favicon 路由 |

## 说明

后三份是执行过程中由子任务各自建立的小计划，内容已全部落地并通过审查。
`plan_NamedTunnel.md` 里的第 10 节起是完整的分步实现计划和执行结果记录。
