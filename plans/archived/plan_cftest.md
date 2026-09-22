# 补 cfResolveZone 和 cfResolveAccount 的测试和错误信息

## 目标
补两个关键的 zone 解析测试和一条改进的错误信息，防止 DNS 记录误建到别人的域名。

## 现状
1. `cfResolveZone` 的后缀匹配逻辑正确（防止 notexample.com 误匹配 example.com）
2. 但缺少两个测试保护：
   - domain == zone name 的完全相等情况
   - lookalike 域名（notexample.com vs example.com）必须被拒绝
3. `cfResolveAccount` 的 0-account 错误信息不够指导

## 实施步骤

### 1. 添加 TestCfResolveZoneExactMatch
- 在 cloudflare_api_test.go 中添加新测试函数
- 假服务器返回 zone: `example.com` (id: `zone-exact-match`)
- 查询 domain: `example.com`
- 断言：返回的 zone ID == `zone-exact-match`

### 2. 添加 TestCfResolveZoneRejectsLookalike（最关键）
- 在 cloudflare_api_test.go 中添加新测试函数
- 假服务器返回 zone: `example.com` (id: `zone-lookalike-test`)
- 查询 domain: `notexample.com`
- 断言：返回 error，不应该匹配

### 3. 改进 cfResolveAccount 错误信息
- 把 cloudflare_api.go 第 230 行的错误信息改为指导性更强的消息
- 提示用户检查 API token 的 Account 权限

### 4. 验证（全部必做，输出放进报告）
- 步骤 a：跑新测试验证通过
- 步骤 b：把 cfResolveZone 的后缀判断故意改坏（验证新测试会失败）
- 步骤 c：把代码改回来，再跑一遍全绿
- 步骤 d：完整 go vet 和 go test

## 关键细节
- 照已有的 TestCfResolveZoneLongestSuffixMatch 写法（httptest.Server 模式）
- 后缀判断的关键是那个 `.` 前缀：`"."+z.Name` vs `z.Name`
- 错误信息保持英文，照已有的两条错误信息的风格
