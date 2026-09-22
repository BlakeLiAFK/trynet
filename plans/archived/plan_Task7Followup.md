# 计划：修复无效测试 + teardown 小瑕疵 + 拆分超长测试文件

## 完成状态：DONE

- commit `8e890b6`：修无效测试 + teardown 小瑕疵
- commit `3874e00`：拆分 cloudflare_api_test.go
- 变异验证：把 `if existing != nil` 改成 `if false` 后
  `TestSetupNamedTunnelReusesEverything` 按预期失败，改回后全绿
- 验证：`gofmt -l .` 无输出，`go vet ./...` 无报错，`go test -count=1 ./...` 全 PASS
- 拆分前测试数 25 = 拆分后 7（`cloudflare_api_test.go`）+ 18（`cloudflare_resources_test.go`）
- 所有 .go 文件行数均 < 500

## 背景

`.superpowers/sdd` 流水线里 task-7（setupNamedTunnel 编排）已经完成并有 report。
这是针对该成果的一轮后续修正，任务描述已经非常具体，直接按给定步骤执行。

## 步骤

1. **修无效测试**：`named_tunnel_test.go` 的 `newFakeCloudflareServer` 在
   `tunnelExists=true` 时，如果 `/accounts/acct1/cfd_tunnel` 收到 POST（创建请求），
   `t.Fatal`。不能破坏 `TestSetupNamedTunnelCreatesEverything`（那个场景 POST 应该发生）。
   做变异验证：把 `named_tunnel.go` 里 `if existing != nil` 改成 `if false`，
   确认 `TestSetupNamedTunnelReusesEverything` 失败；改回来，确认全绿。

2. **teardown 小瑕疵**：最后一次重试失败后不要再 `sleepCtx`，直接退出循环。
   同时在 `setupNamedTunnel` 注释里补充：创建成功但后续失败不会产生孤儿隧道，
   因为隧道名由域名确定性推导，下次重跑会按名字复用。

3. **拆分 `cloudflare_api_test.go`**（586 行，超过 500 行上限）：
   - 保留：`cfDo`/`cfDoFull`/`cfList`/`cfPath`/`cfError`/`isCFNotFound`/
     `sanitizeTunnelName` 相关测试
   - 新建 `cloudflare_resources_test.go`：account/zone 解析、tunnel 查找创建取
     token、DNS 记录增删查 相关测试
   - 纯粹搬代码，不改一行测试逻辑，两个文件都要 < 500 行

## 验证

```
GOTOOLCHAIN=go1.26.8 gofmt -l .
GOTOOLCHAIN=go1.26.8 go vet ./...
GOTOOLCHAIN=go1.26.8 go test -count=1 ./...
wc -l *.go
```

拆分前后测试总数一致。

## Git

按改动性质分两个 commit：一个是"修测试 + teardown 瑕疵"，一个是"纯拆文件"。
