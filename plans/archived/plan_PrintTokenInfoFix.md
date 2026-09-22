# 修复计划：printTokenInfo 函数缺陷修复

## 背景

`banner.go` 中的 `printTokenInfo(token string)` 函数存在缺陷：它自己读取环境变量来判断 token 来源，但项目中命令行 flag 优先于环境变量，当两者都设置时会显示错误的来源信息。

## 问题分析

- 当前函数检查 `os.Getenv("CLOUDFLARE_API_TOKEN")` 是否为空来判断来源
- 实际项目规则：命令行 flag `-api-token` 优先于环境变量
- 结果：flag 设置时，函数仍会错误地显示 `(from CLOUDFLARE_API_TOKEN)`

## 修复方案

### 1. 修改函数签名（banner.go）
- 从 `printTokenInfo(token string)` 改为 `printTokenInfo(token, source string)`
- 调用方负责传递真实的 token 来源

### 2. 修改函数实现（banner.go）
- token 为空时分支保持不变：显示 `none` 和设置提示
- token 非空时：直接使用传进来的 `source` 参数拼接 `(from %s)`
- 移除自我读取环境变量的逻辑

### 3. 更新测试（banner_test.go）
- 修改 `TestPrintTokenInfoMissing`：适配新签名，`source` 参数可忽略（token 为空时不用）
- 修改 `TestPrintTokenInfoSet`：适配新签名
- **新增** `TestPrintTokenInfoSourceFlag`：传 `source = "-api-token"`，验证输出包含此来源
- **新增** `TestPrintTokenInfoSourceEnv`：传 `source = "CLOUDFLARE_API_TOKEN"`，验证输出包含此来源
- 保留现有断言：完整 token 不能出现在输出中

### 4. 验证步骤
1. 运行单元测试：`go test -run 'TestMaskSecret|TestPrintTokenInfo' -v .`
2. 运行代码检查：`go vet ./...`
3. 运行全部测试：`go test -count=1 ./...`

### 5. 提交
- 提交 banner.go 和 banner_test.go 的改动
- 不涉及 main.go（调用点在后续任务）
- 遵守项目规范：代码注释中文，日志和用户可见输出英文

## 预期结果
- 函数签名更清晰，职责单一
- 测试覆盖率提高，新增缺失分支的覆盖
- 所有测试和代码检查通过
