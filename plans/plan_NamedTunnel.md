# plan_NamedTunnel —— trynet 固定域名模式

## 0. 背景

现有 trynet 只支持 quick tunnel（零认证、随机域名、跑一次就没了）。这次加一个"固定域名"
模式：用户自己的域名 + Cloudflare API Token，trynet 自动在 Cloudflare 后台建好 named
tunnel、配好 DNS，然后用已有的连接/注册/优雅退出/退避重连逻辑跑起来——这部分代码一行不改。

## 1. 决策记录（brainstorming 过程中定下来的，按顺序）

1. **身份解析全自动**：Account ID、Zone ID 都从 API Token 自己反查，不需要用户手动查后台。
2. **API Token 只走环境变量 + flag，不做交互式输入**：`-api-token` / `CLOUDFLARE_API_TOKEN`，
   flag 优先。不引入任何交互式兜底或 TUI —— TUI 会让工具没法在 CI/脚本里非交互跑，
   跟"自动化工具"这个定位直接冲突，讨论过后否决。
3. **同名 tunnel/DNS 自动复用**：不重复创建，也不主动覆盖指向别处的 DNS 记录（发现冲突
   直接报错，不抢用户的 DNS）。
4. **`-teardown`**：可选 flag，程序优雅退出后顺带删掉这次自动创建/复用的 tunnel + DNS
   记录，用于临时测试场景。
5. **不引入 cobra**：6 个平铺参数，没有子命令树，标准库 `flag` 完全够用；cobra 解决的是
   参数声明问题，跟这次真正的复杂度来源（Cloudflare API 编排）无关，还会让二进制变大，
   跟项目"精简"的一贯取向拧着，否决。
6. **不引入 TUI 框架**：讨论过 bubbletea 方案，体验更好但新增一整套依赖、二进制明显变大、
   工具性质从"迷你 CLI"变成"终端应用"；且真正要收集的信息只有域名和 token 两项，用
   TUI 框架处理杀鸡用牛刀。最终否决，改用"参数/环境变量给全了就零交互，不追加任何
   交互层"。
7. **全部参数统一 `flag > 环境变量 > 默认值`**：不只是 token，`-port`/`-dir` 等现有参数
   也补齐环境变量支持，规则统一、好记。Cloudflare 相关的两个环境变量名直接用生态
   已有约定（`CLOUDFLARE_API_TOKEN`、`CLOUDFLARE_ACCOUNT_ID`，wrangler/terraform-provider
   都用这两个名字，用户可能已经设置过，不用再学新名字）。
8. **banner 下面加一行脱敏回显**：跟现有 `proxy` 那行同一视觉模式，`-domain` 模式下额外
   打印 token 来源和掩码后的值，强化"环境变量真的读到了"的认知；没设时给出清楚的报错
   提示该设哪个变量。

## 2. 最终参数表

| 参数 | flag | 环境变量 | 类型 | 默认值 |
| --- | --- | --- | --- | --- |
| 端口 | `-port` | `TRYNET_PORT` | int | 0 |
| 目录 | `-dir` | `TRYNET_DIR` | string | ""（都不给时代码里再兜底成 `.`） |
| 固定域名 | `-domain` | `TRYNET_DOMAIN` | string | "" |
| API Token | `-api-token` | `CLOUDFLARE_API_TOKEN` | string | "" |
| Account ID | `-account-id` | `CLOUDFLARE_ACCOUNT_ID` | string | ""（逃生舱，token 只关联一个 account 时自动探测，不用填） |
| 退出清理 | `-teardown` | `TRYNET_TEARDOWN` | bool | false |

统一实现：`envOr(name, def string) string`（+ `envOrInt`/`envOrBool` 变体）把环境变量的值
当成 `flag.XxxVar` 注册时的默认值，`flag.Parse()` 后用户传了 flag 自然覆盖 —— 全部参数
一套逻辑，不特殊处理任何一个。

## 3. 整体流程

`-domain` 非空即进入固定域名模式，跳过 `requestQuickTunnel()`：

```
① 解析身份
   GET /accounts                     → 只有一个就用；多个且没给 -account-id 就报错列出来
   GET /zones                        → 在 token 能访问的 zone 里找 <domain> 的最长后缀匹配
                                        （domain=files.example.com 匹配到 zone=example.com）

② 找到/建好 tunnel
   tunnel 名字 = "trynet-" + 域名清洗成合法字符
                 （Cloudflare tunnel 名字只能 [a-z0-9-]，files.example.com
                  → trynet-files-example-com；前缀是为了 -teardown 只删自己建的东西）
   GET  /accounts/{a}/cfd_tunnel?name=...&is_deleted=false  → 存在就复用 id
   POST /accounts/{a}/cfd_tunnel {name, config_src:"cloudflare"}  → 不存在就建
        （不自己生成 tunnel_secret，交给 Cloudflare 管理）
   GET  /accounts/{a}/cfd_tunnel/{id}/token  → 不管新建还是复用，统一走这一步拿 token
        （这个接口对已存在的 tunnel 也能随时重新要，不需要在本地存密钥）

③ 对好 DNS
   GET  /zones/{z}/dns_records?type=CNAME&name=<domain>
     → 已存在且 content == <tunnel-id>.cfargotunnel.com：跳过
     → 已存在但指向别处：报错，不抢着改
     → 不存在：POST 建一条 CNAME，proxied=true（必须橙云，灰云连不通隧道）

④ 复用现有代码
   token 解出来的 {AccountTag, TunnelSecret, TunnelID} 直接塞进现有 tunnelInfo，
   Hostname 就是用户给的 -domain。dialEdge/serve/run/优雅退出/退避重连一行不改。
```

## 4. 新文件 `cloudflare_api.go`（预计 ~200 行）

```go
sanitizeTunnelName(domain string) string

cfListAccounts(token string) ([]cfAccount, error)
cfResolveZone(token, domain string) (cfZone, error)          // 最长后缀匹配

cfFindTunnel(token, accountID, name string) (*cfTunnel, error)  // 不存在返回 nil，不报错
cfCreateTunnel(token, accountID, name string) (cfTunnel, error)
cfGetTunnelToken(token, accountID, tunnelID string) (*tunnelInfo, error)  // 复用现有结构体

cfFindDNSRecord(token, zoneID, name string) (*cfDNSRecord, error)
cfCreateDNSRecord(token, zoneID, domain, target string) (cfDNSRecord, error)
cfDeleteDNSRecord(token, zoneID, recordID string) error
cfDeleteTunnel(token, accountID, tunnelID string) error

setupNamedTunnel(token, accountIDHint, domain string) (info *tunnelInfo, teardown func() error, err error)
```

`setupNamedTunnel` 编排以上全部，`main()` 只调这一个函数。HTTP 客户端复用
`requestQuickTunnel()` 里那个代理感知的 `http.Client{Transport: &http.Transport{Proxy:
http.ProxyFromEnvironment}}`，不加新依赖，标准库 `net/http` + `encoding/json` 足够。

Cloudflare API 响应统一是 `{success, errors:[{code,message}], result}`，出错时把
`errors` 原样透出而不是包一层模糊的 Go error——方便用户照着改 token 权限。

## 5. `main.go` 改动

1. `run()` 签名加一个 `refresh func() (*tunnelInfo, error)` 参数：
   - quick tunnel 模式传 `requestQuickTunnel`
   - named tunnel 模式传一个轻量闭包，只重新 `cfGetTunnelToken()`（tunnel/DNS 已经在了，
     注册被拒绝时不用重新走一遍建 tunnel/建 DNS）
2. `main()` 根据 `*domain != ""` 二选一调 `requestQuickTunnel()` 或 `setupNamedTunnel()`。
3. `-teardown` 在 `run()` 因 Ctrl+C 正常退出（优雅退出 RPC 跑完）之后才触发删除，
   不影响现有关闭顺序。

## 6. banner 新增一行（`banner.go`）

只在 `-domain` 模式下打印，位置在现有 `proxy` 那行下面：

```
  proxy      socks5://192.168.3.181:1080  (from ALL_PROXY)
  cf token   AbCd…9wXz  (from CLOUDFLARE_API_TOKEN)
```

没设时：

```
  cf token   none — set CLOUDFLARE_API_TOKEN (or -api-token) to use -domain
```

掩码规则：`maskSecret(s)` —— 长度 <=8 全部替换成 `*`；否则前 4 位 + `…` + 后 4 位，
中间隐藏。纯粹给用户一个"是不是我以为的那个 token"的视觉锚点，不追求安全强度
（4+4 位不影响一个高熵随机串的安全性）。

## 6.1 边界情况

- `-teardown` 但没给 `-domain`：纯粹的 no-op，quick tunnel 模式没有云端资源好清理，
  不报错也不提示，安静忽略。
- 项目目前不是 git 仓库（`git status` 已确认），brainstorming skill 默认"提交 spec
  到 git"这一步跳过，spec 只落盘在 `plans/plan_NamedTunnel.md`。

## 7. 明确不做

- 不引入 cobra、不引入任何 TUI 框架（见决策记录 5、6）
- 不做交互式 token 输入兜底（见决策记录 2）
- 不自动删除"不是自己创建"的 tunnel/DNS（`-teardown` 只认 `trynet-` 前缀命名的 tunnel）
- 不支持多 zone/多域名一次跑（一次进程只服务一个 `-domain`）
- 不做 token 本地持久化/缓存（每次都读环境变量或 flag，不写文件）

## 8. 测试计划

- `sanitizeTunnelName`：表驱动测试，覆盖大写、点号、非法字符、超长截断
- `maskSecret`：短串全遮、长串首尾各 4 位
- `envOr`/`envOrInt`/`envOrBool`：flag 优先于 env 的覆盖顺序
- Cloudflare API 函数：起一个 `httptest.Server` 模拟响应（成功、404 找不到、
  403 权限不足、多 account 冲突），不依赖真实网络和真实 token
- `setupNamedTunnel` 的编排逻辑：基于上面的 fake server 跑一遍"全新创建"和
  "全部复用"两条路径
- 真实网络端到端：需要用户提供一个真实 Cloudflare 账号 + API Token + 域名才能测，
  这部分只能等实现完成后由用户手动验证一次，无法在当前环境里自动跑通

## 9. 进度

- [x] brainstorming 完成，设计定稿
- [x] 用户 review 本 spec
- [x] 转 writing-plans 细化实现步骤（见第 10 节）
- [x] 编码（Task 1-8 全部完成，12 个 commit）
- [x] 单元测试（fake server）全绿：45 个测试函数
- [x] 快速隧道模式真实网络回归通过（未受接线影响）
- [ ] **用户提供真实 Cloudflare 账号做固定域名模式的端到端验证**（唯一剩余项）

### 执行结果

12 个 commit，从 `41b94f6`（基线）到 `3352ab8`。

| Task | 内容 | review 挡下的问题 |
| --- | --- | --- |
| 1 | 环境变量 helper | — |
| 2 | banner token 脱敏行 | token 来源误判（flag 优先时显示成环境变量） |
| 3 | CF API 地基 | **分页缺失**：域名 >20 个的用户查不到自己的 zone；外加封装加固 |
| 4 | 账号/zone 解析 | 最长后缀匹配无测试保护（补测试 + 变异验证） |
| 5 | 隧道查找/创建/取 token | — |
| 6 | DNS 操作 | **删除不幂等**：CF 删不存在的记录返回 81044，teardown 会误报失败 |
| 7 | setupNamedTunnel 编排 | **假测试**：复用场景与创建场景返回同一 id，实现退化也能通过 |
| 8 | main.go 接线 | — |

文件划分（全部 <500 行）：

```
main.go                      283   参数解析、模式选择、重连循环
tunnel.go                    268   单条边缘连接的生命周期
cloudflare_api.go            415   Cloudflare 单个 API 调用的封装
named_tunnel.go              127   固定域名模式的编排与清理
banner.go                    140   启动横幅与状态回显
proxy.go                     188   代理感知的拨号
```

产物：`trynet-raw.exe` 13.00 MB，UPX 后 `trynet.exe` 4.30 MB。

### 两次"假测试"的教训

Task 4 和 Task 7 各出现一次**看起来在测、实际什么也没验证**的测试：
断言的东西恰好在正确实现和错误实现下都成立。两次都是 reviewer 手工推演才发现的。

此后所有补测试的修复一律要求**变异验证**：把实现临时改坏，确认测试真的变红，
再改回来，两次输出都进报告。光看"新增测试通过"证明不了测试有鉴别力。

---

# trynet 固定域名模式 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 给 trynet 加一个 `-domain` 模式：自动通过 Cloudflare API 建好 named tunnel +
DNS 记录，接入现有的连接/注册/优雅退出/退避重连逻辑，跑出一个固定域名的隧道。

**Architecture:** 新增 `cloudflare_api.go` 做 Cloudflare 管理 API 的编排（账号/zone 解析、
tunnel 查找/创建、DNS 查找/创建/删除），产出一个跟 quick tunnel 完全同构的 `*tunnelInfo`，
交给现有的 `dialEdge`/`serve`/`run` 跑——这部分连接层代码不改一行。`main.go` 只需要多一
个"用哪种方式拿到 tunnelInfo"的分支，以及给 `run()` 加一个失败时怎么刷新凭据的回调。

**Tech Stack:** Go 1.26.8，仅标准库 `net/http` + `encoding/json`，不引入新依赖。

## Global Constraints（照抄 spec，每个任务都要遵守）

- 不引入 cobra、不引入任何 TUI 框架（spec 决策记录 5、6）
- API Token 只走 `-api-token` flag / `CLOUDFLARE_API_TOKEN` 环境变量，flag 优先，
  不做交互式输入兜底（spec 决策记录 2）
- 所有参数统一 `flag > 环境变量 > 默认值`（spec 决策记录 7）
- 同名 tunnel/DNS 自动复用；DNS 记录指向别处时报错，不覆盖（spec 决策记录 3）
- `-teardown` 只删自己创建/复用的资源，只在优雅退出完成之后触发（spec 决策记录 4）
- 日志/错误信息全英文（沿用项目现有规矩，见 plans/plan_MiniTunnel.md 第 13 节）
- 代码注释中文，风格跟现有 `main.go`/`proxy.go`/`banner.go` 保持一致
- 单文件不超过 500 行
- 项目当前不是 git 仓库：开始编码前先 `git init`（只建本地仓库，不涉及 remote/push，
  不违反 CLAUDE.md 的 push 限制），这样每个任务末尾的 commit 步骤才有意义

---

### Task 1: 环境变量 helper + 参数接线

**Files:**
- Modify: `main.go`
- Test: `main_test.go`

**Interfaces:**
- Produces: `envOr(name, def string) string`、`envOrInt(name string, def int) int`、
  `envOrBool(name string, def bool) bool`（供后续所有任务的 flag 注册使用）
- Produces: `-port`/`-dir` 的默认值改为从 `TRYNET_PORT`/`TRYNET_DIR` 读取
  （其余四个新参数在 Task 8 跟使用逻辑一起加，避免出现声明了没人用的占位变量）

- [ ] **Step 1: 写 envOr/envOrInt/envOrBool 的失败测试**

在 `main_test.go` 末尾追加：

```go
func TestEnvOr(t *testing.T) {
	t.Setenv("TRYNET_TEST_STR", "")
	if got := envOr("TRYNET_TEST_STR", "def"); got != "def" {
		t.Errorf("empty env should fall back to default, got %q", got)
	}
	t.Setenv("TRYNET_TEST_STR", "set")
	if got := envOr("TRYNET_TEST_STR", "def"); got != "set" {
		t.Errorf("got %q, want %q", got, "set")
	}
}

func TestEnvOrInt(t *testing.T) {
	t.Setenv("TRYNET_TEST_INT", "")
	if got := envOrInt("TRYNET_TEST_INT", 7); got != 7 {
		t.Errorf("got %d, want 7", got)
	}
	t.Setenv("TRYNET_TEST_INT", "42")
	if got := envOrInt("TRYNET_TEST_INT", 7); got != 42 {
		t.Errorf("got %d, want 42", got)
	}
	t.Setenv("TRYNET_TEST_INT", "not-a-number")
	if got := envOrInt("TRYNET_TEST_INT", 7); got != 7 {
		t.Errorf("invalid env should fall back to default, got %d", got)
	}
}

func TestEnvOrBool(t *testing.T) {
	t.Setenv("TRYNET_TEST_BOOL", "")
	if got := envOrBool("TRYNET_TEST_BOOL", false); got != false {
		t.Errorf("got %v, want false", got)
	}
	t.Setenv("TRYNET_TEST_BOOL", "true")
	if got := envOrBool("TRYNET_TEST_BOOL", false); got != true {
		t.Errorf("got %v, want true", got)
	}
}
```

- [ ] **Step 2: 跑测试确认失败**

Run: `GOTOOLCHAIN=go1.26.8 go test -run 'TestEnvOr' -v .`
Expected: FAIL，`envOr`/`envOrInt`/`envOrBool` undefined

- [ ] **Step 3: 实现三个 helper**

在 `main.go` 里，`main()` 函数上方加：

```go
// envOr 返回环境变量的值，没设置（或是空串）就用 def。
func envOr(name, def string) string {
	if v := os.Getenv(name); v != "" {
		return v
	}
	return def
}

func envOrInt(name string, def int) int {
	if v := os.Getenv(name); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return def
}

func envOrBool(name string, def bool) bool {
	if v := os.Getenv(name); v != "" {
		if b, err := strconv.ParseBool(v); err == nil {
			return b
		}
	}
	return def
}
```

`import` 里加 `"strconv"`（`os` 已经在现有 import 列表里）。

- [ ] **Step 4: 跑测试确认通过**

Run: `GOTOOLCHAIN=go1.26.8 go test -run 'TestEnvOr' -v .`
Expected: PASS（3 个测试全过）

- [ ] **Step 5: 把现有两个 flag 的默认值改成 env-aware**

把 `main()` 里原来的：

```go
	port := flag.Int("port", 0, "local port to expose")
	dir := flag.String("dir", "", "local directory to share (served on a random port)")
```

替换成：

```go
	port := flag.Int("port", envOrInt("TRYNET_PORT", 0), "local port to expose")
	dir := flag.String("dir", envOr("TRYNET_DIR", ""), "local directory to share (served on a random port)")
```

（`flag.Parse()` 那行不动。）

**本任务只改这两个已有参数**：`-domain`/`-api-token`/`-account-id`/`-teardown`
这四个新参数留到 Task 8 跟它们的使用逻辑一起加——Go 里声明了不用的局部变量是编译
错误，提前声明就得写一行 `_ = xxx` 的占位垃圾，没必要。

- [ ] **Step 6: 跑全量测试和 vet 确认没有破坏现有功能**

Run: `GOTOOLCHAIN=go1.26.8 go vet ./... && GOTOOLCHAIN=go1.26.8 go build ./... && GOTOOLCHAIN=go1.26.8 go test -count=1 ./...`
Expected: 全部 PASS

- [ ] **Step 7: Commit**

```bash
git add main.go main_test.go
git commit -m "Add env-aware flag default helpers"
```

---

### Task 2: banner 新增 token 回显行

**Files:**
- Modify: `banner.go`
- Test: `banner_test.go`（新建）

**Interfaces:**
- Consumes: `banner.go` 已有的 `bold`/`green`/`yellow`/`dim`/`reset` 颜色变量
- Produces: `maskSecret(s string) string`、`printTokenInfo(token, source string)`
  （`main.go` 在 Task 8 里调用；来源由调用方传入，banner 不自己猜——
  flag 优先于环境变量，banner 光看值分不出来源）

- [ ] **Step 1: 写 maskSecret 的失败测试**

新建 `banner_test.go`：

```go
package main

import (
	"bytes"
	"io"
	"os"
	"strings"
	"testing"
)

func TestMaskSecret(t *testing.T) {
	cases := map[string]string{
		"":                 "",
		"short":            "*****",
		"12345678":         "********",
		"123456789":        "1234…6789",
		"AbCdEfGhIjKlMnOp": "AbCd…MnOp",
	}
	for in, want := range cases {
		if got := maskSecret(in); got != want {
			t.Errorf("maskSecret(%q) = %q, want %q", in, got, want)
		}
	}
}

// captureStdout 跑 f 的时候把 os.Stdout 换成一根管道，收集写出来的内容。
// banner.go 里的打印函数都是直接往 os.Stdout 写，这是验证它们输出内容的唯一办法。
func captureStdout(t *testing.T, f func()) string {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	old := os.Stdout
	os.Stdout = w
	defer func() { os.Stdout = old }()

	f()

	_ = w.Close()
	var buf bytes.Buffer
	_, _ = io.Copy(&buf, r)
	return buf.String()
}

func TestPrintTokenInfoMissing(t *testing.T) {
	out := captureStdout(t, func() { printTokenInfo("") })
	if !strings.Contains(out, "none") || !strings.Contains(out, "CLOUDFLARE_API_TOKEN") {
		t.Errorf("missing-token banner line looks wrong: %q", out)
	}
}

func TestPrintTokenInfoSet(t *testing.T) {
	t.Setenv("CLOUDFLARE_API_TOKEN", "AbCdEfGhIjKlMnOp")
	out := captureStdout(t, func() { printTokenInfo("AbCdEfGhIjKlMnOp") })
	if !strings.Contains(out, "AbCd…MnOp") {
		t.Errorf("token should be masked in output: %q", out)
	}
	if strings.Contains(out, "AbCdEfGhIjKlMnOp") {
		t.Errorf("full token must never be printed: %q", out)
	}
}
```

- [ ] **Step 2: 跑测试确认失败**

Run: `GOTOOLCHAIN=go1.26.8 go test -run 'TestMaskSecret|TestPrintTokenInfo' -v .`
Expected: FAIL，`maskSecret`/`printTokenInfo` undefined

- [ ] **Step 3: 实现 maskSecret 和 printTokenInfo**

在 `banner.go` 的 `printReady` 函数下面追加：

```go
// printTokenInfo 打印脱敏后的 API token 来源，跟上面 printProxyInfo 是同一视觉风格，
// 只在 -domain 模式下调用，给用户一个"环境变量真的读到了"的锚点。
func printTokenInfo(token string) {
	if token == "" {
		fmt.Printf("  %scf token%s  %snone%s — set %sCLOUDFLARE_API_TOKEN%s (or -api-token) to use -domain\n\n",
			bold, reset, yellow, reset, green, reset)
		return
	}
	source := "CLOUDFLARE_API_TOKEN"
	if os.Getenv("CLOUDFLARE_API_TOKEN") == "" {
		source = "-api-token"
	}
	fmt.Printf("  %scf token%s  %s%s%s  %s(from %s)%s\n\n",
		bold, reset, green, maskSecret(token), reset, dim, source, reset)
}

// maskSecret 只在展示时用：8 位以内全部打星号，更长的留头尾各 4 位、中间用 … 隐藏。
// 不追求安全强度（前后各 4 位不影响一个高熵随机串的安全性），纯粹给用户一个
// "是不是我以为的那个值"的视觉锚点。
func maskSecret(s string) string {
	if len(s) <= 8 {
		return strings.Repeat("*", len(s))
	}
	return s[:4] + "…" + s[len(s)-4:]
}
```

- [ ] **Step 4: 跑测试确认通过**

Run: `GOTOOLCHAIN=go1.26.8 go test -run 'TestMaskSecret|TestPrintTokenInfo' -v .`
Expected: PASS（3 个测试全过）

- [ ] **Step 5: 跑全量测试确认没有破坏现有功能**

Run: `GOTOOLCHAIN=go1.26.8 go vet ./... && GOTOOLCHAIN=go1.26.8 go test -count=1 ./...`
Expected: 全部 PASS

- [ ] **Step 6: Commit**

```bash
git add banner.go banner_test.go
git commit -m "Add masked API token banner line"
```

---

### Task 3: cloudflare_api.go 骨架 —— 类型、通用请求函数、tunnel 名字清洗

**Files:**
- Create: `cloudflare_api.go`
- Create: `cloudflare_api_test.go`
- Modify: `main.go`（把 `requestQuickTunnel` 里的 http.Client 提成包级变量，供两边共用）

**Interfaces:**
- Consumes: `main.go` 的 `apiHTTPClient`（本任务从 `requestQuickTunnel` 里提取出来）
- Produces: `cfResponse[T]`、`cfAPIError`、`cfDo[T any](ctx, method, path, token string, body any) (T, error)`、
  `sanitizeTunnelName(domain string) string`（后续任务都基于 `cfDo` 实现）

- [ ] **Step 1: 把 http.Client 提成包级共享变量**

`main.go` 里找到 `requestQuickTunnel`，把：

```go
	// 这里用标准库的代理支持即可，https_proxy 会被自动识别
	client := &http.Client{
		Timeout:   30 * time.Second,
		Transport: &http.Transport{Proxy: http.ProxyFromEnvironment},
	}
	resp, err := client.Do(req)
```

改成：

```go
	resp, err := apiHTTPClient.Do(req)
```

然后在 `requestQuickTunnel` 函数上方（`const` 块之后）加一个包级变量：

```go
// apiHTTPClient 是所有"管理面"HTTP 调用（trycloudflare API、Cloudflare 管理 API）
// 共用的客户端：会自动识别 https_proxy 之类的环境变量，跟隧道数据面的连接（proxy.go
// 里那套手写 CONNECT/socks5）是两回事，互不影响。
var apiHTTPClient = &http.Client{
	Timeout:   30 * time.Second,
	Transport: &http.Transport{Proxy: http.ProxyFromEnvironment},
}
```

- [ ] **Step 2: 确认现有测试仍然通过（这步只是重构，不改行为）**

Run: `GOTOOLCHAIN=go1.26.8 go vet ./... && GOTOOLCHAIN=go1.26.8 go test -count=1 ./...`
Expected: 全部 PASS

- [ ] **Step 3: 写 cfDo 和 sanitizeTunnelName 的失败测试**

新建 `cloudflare_api_test.go`：

```go
package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestSanitizeTunnelName(t *testing.T) {
	cases := map[string]string{
		"files.example.com":     "trynet-files-example-com",
		"Files.EXAMPLE.com":     "trynet-files-example-com",
		"a..b---c.com":          "trynet-a-b-c-com",
		"a-very-long-subdomain-name-that-goes-on-and-on-and-on.example.com": "",
	}
	for in, want := range cases {
		got := sanitizeTunnelName(in)
		if want != "" && got != want {
			t.Errorf("sanitizeTunnelName(%q) = %q, want %q", in, got, want)
		}
		if len(got) > 63 {
			t.Errorf("sanitizeTunnelName(%q) too long: %d chars", in, len(got))
		}
		if got[0] == '-' || got[len(got)-1] == '-' {
			t.Errorf("sanitizeTunnelName(%q) = %q has leading/trailing dash", in, got)
		}
		for _, r := range got {
			if !(r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '-') {
				t.Errorf("sanitizeTunnelName(%q) = %q has illegal char %q", in, got, r)
			}
		}
	}
}

func TestCfDoSuccess(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer test-token" {
			t.Errorf("missing/wrong Authorization header: %q", got)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"success": true,
			"errors":  []any{},
			"result":  map[string]string{"id": "abc123"},
		})
	}))
	defer srv.Close()

	cfAPIBase = srv.URL
	defer func() { cfAPIBase = "https://api.cloudflare.com/client/v4" }()

	type result struct {
		ID string `json:"id"`
	}
	got, err := cfDo[result](context.Background(), http.MethodGet, "/whatever", "test-token", nil)
	if err != nil {
		t.Fatal(err)
	}
	if got.ID != "abc123" {
		t.Errorf("got %+v", got)
	}
}

func TestCfDoAPIError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"success": false,
			"errors": []map[string]any{
				{"code": 9109, "message": "Invalid access token"},
			},
			"result": nil,
		})
	}))
	defer srv.Close()

	cfAPIBase = srv.URL
	defer func() { cfAPIBase = "https://api.cloudflare.com/client/v4" }()

	_, err := cfDo[map[string]any](context.Background(), http.MethodGet, "/whatever", "bad-token", nil)
	if err == nil {
		t.Fatal("expected an error")
	}
	if !strings.Contains(err.Error(), "Invalid access token") {
		t.Errorf("error should surface the Cloudflare message, got: %v", err)
	}
}
```

- [ ] **Step 4: 跑测试确认失败**

Run: `GOTOOLCHAIN=go1.26.8 go test -run 'TestSanitizeTunnelName|TestCfDo' -v .`
Expected: FAIL，`cloudflare_api.go` 里的符号都不存在，编译错误

- [ ] **Step 5: 实现 cloudflare_api.go 骨架**

新建 `cloudflare_api.go`：

```go
// cloudflare_api.go 是 Cloudflare 管理 API 的最小封装：账号/zone 解析、
// named tunnel 的查找/创建、DNS 记录的查找/创建/删除。
// 只用来给 -domain 模式建好云端资源，拿到 token 之后就交回给 main.go 里
// 现成的连接逻辑，这个文件不碰隧道协议本身。
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// cfAPIBase 是个变量而不是常量，方便测试时指向本地 httptest.Server
var cfAPIBase = "https://api.cloudflare.com/client/v4"

// cfResponse 是 Cloudflare API 统一的响应外壳
type cfResponse[T any] struct {
	Success bool         `json:"success"`
	Errors  []cfAPIError `json:"errors"`
	Result  T            `json:"result"`
}

type cfAPIError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type cfAccount struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

type cfZone struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

type cfTunnel struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

type cfDNSRecord struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Type    string `json:"type"`
	Content string `json:"content"`
	Proxied bool   `json:"proxied"`
}

// cfDo 发一个 Cloudflare API 请求，把 result 字段解到 T 里。
// API 返回 success=false 时，把 errors 数组原样拼成一句话报出去，
// 用户照着这句话就知道该去补哪个 token 权限，不用来回猜一层包装过的 Go error。
func cfDo[T any](ctx context.Context, method, path, token string, body any) (T, error) {
	var zero T

	var reqBody io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return zero, err
		}
		reqBody = bytes.NewReader(b)
	}

	req, err := http.NewRequestWithContext(ctx, method, cfAPIBase+path, reqBody)
	if err != nil {
		return zero, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := apiHTTPClient.Do(req)
	if err != nil {
		return zero, err
	}
	defer resp.Body.Close()

	var parsed cfResponse[T]
	if err := json.NewDecoder(resp.Body).Decode(&parsed); err != nil {
		return zero, fmt.Errorf("cloudflare api: decode response: %w (http %s)", err, resp.Status)
	}
	if !parsed.Success {
		return zero, fmt.Errorf("cloudflare api: %s", formatCFErrors(parsed.Errors, resp.StatusCode))
	}
	return parsed.Result, nil
}

func formatCFErrors(errs []cfAPIError, status int) string {
	if len(errs) == 0 {
		return fmt.Sprintf("http %d", status)
	}
	parts := make([]string, len(errs))
	for i, e := range errs {
		parts[i] = fmt.Sprintf("%s (code %d)", e.Message, e.Code)
	}
	return strings.Join(parts, "; ")
}

// sanitizeTunnelName 把域名变成 Cloudflare tunnel 名字允许的格式：
// 只能小写字母、数字、连字符，首尾不能是连字符，1-63 字符。
// files.example.com -> trynet-files-example-com
// 前缀 trynet- 是故意的：-teardown 只删这个前缀开头的 tunnel，不动 dashboard 里
// 其它不相关的 tunnel。
func sanitizeTunnelName(domain string) string {
	const prefix = "trynet-"
	var b strings.Builder
	b.WriteString(prefix)
	prevDash := false
	for _, r := range strings.ToLower(domain) {
		switch {
		case r >= 'a' && r <= 'z' || r >= '0' && r <= '9':
			b.WriteRune(r)
			prevDash = false
		default:
			if !prevDash {
				b.WriteByte('-')
				prevDash = true
			}
		}
	}
	name := strings.TrimRight(b.String(), "-")
	const maxLen = 63
	if len(name) > maxLen {
		name = strings.TrimRight(name[:maxLen], "-")
	}
	return name
}
```

- [ ] **Step 6: 跑测试确认通过**

Run: `GOTOOLCHAIN=go1.26.8 go test -run 'TestSanitizeTunnelName|TestCfDo' -v .`
Expected: PASS（3 个测试全过）

- [ ] **Step 7: 跑全量测试和 vet**

Run: `GOTOOLCHAIN=go1.26.8 go vet ./... && GOTOOLCHAIN=go1.26.8 go build ./... && GOTOOLCHAIN=go1.26.8 go test -count=1 ./...`
Expected: 全部 PASS

- [ ] **Step 8: Commit**

```bash
git add main.go cloudflare_api.go cloudflare_api_test.go
git commit -m "Add Cloudflare API request helper and tunnel name sanitizer"
```

---

### Task 4: 账号 / zone 解析

**Files:**
- Modify: `cloudflare_api.go`
- Modify: `cloudflare_api_test.go`

**Interfaces:**
- Consumes: Task 3 的 `cfDo`、`cfAccount`、`cfZone`
- **必须用 Task 3 铺好的地基**：list 类接口一律走 `cfList`（它会翻页，直接用 `cfDo`
  只能拿到第一页，域名/账号多的用户会被漏掉）；路径一律用 `pathXxx()` 函数，
  不要自己 `fmt.Sprintf` 拼；`CNAME`/`cloudflare`/`.cfargotunnel.com` 用
  `dnsRecordType`/`tunnelConfigSrc`/`tunnelDNSSuffix` 常量
- Produces: `cfListAccounts(ctx, token) ([]cfAccount, error)`、
  `cfResolveAccount(ctx, token, hint string) (string, error)`、
  `cfListZones(ctx, token) ([]cfZone, error)`、
  `cfResolveZone(ctx, token, domain string) (cfZone, error)`

- [ ] **Step 1: 写失败测试**

在 `cloudflare_api_test.go` 末尾追加：

```go
func TestCfResolveAccountSingleAccount(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"success": true, "errors": []any{},
			"result": []map[string]string{{"id": "acct1", "name": "My Account"}},
		})
	}))
	defer srv.Close()
	cfAPIBase = srv.URL
	defer func() { cfAPIBase = "https://api.cloudflare.com/client/v4" }()

	got, err := cfResolveAccount(context.Background(), "tok", "")
	if err != nil {
		t.Fatal(err)
	}
	if got != "acct1" {
		t.Errorf("got %q, want acct1", got)
	}
}

func TestCfResolveAccountMultipleWithoutHint(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"success": true, "errors": []any{},
			"result": []map[string]string{
				{"id": "acct1", "name": "First"},
				{"id": "acct2", "name": "Second"},
			},
		})
	}))
	defer srv.Close()
	cfAPIBase = srv.URL
	defer func() { cfAPIBase = "https://api.cloudflare.com/client/v4" }()

	_, err := cfResolveAccount(context.Background(), "tok", "")
	if err == nil {
		t.Fatal("expected an error asking the user to pick an account")
	}
	if !strings.Contains(err.Error(), "acct1") || !strings.Contains(err.Error(), "acct2") {
		t.Errorf("error should list both accounts, got: %v", err)
	}
}

func TestCfResolveAccountWithHint(t *testing.T) {
	// 给了 hint 就直接用，不应该发任何请求
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("should not call the API when a hint is given")
	}))
	defer srv.Close()
	cfAPIBase = srv.URL
	defer func() { cfAPIBase = "https://api.cloudflare.com/client/v4" }()

	got, err := cfResolveAccount(context.Background(), "tok", "hinted-acct")
	if err != nil {
		t.Fatal(err)
	}
	if got != "hinted-acct" {
		t.Errorf("got %q, want hinted-acct", got)
	}
}

func TestCfResolveZoneLongestSuffixMatch(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"success": true, "errors": []any{},
			"result": []map[string]string{
				{"id": "zone1", "name": "example.com"},
				{"id": "zone2", "name": "sub.example.com"},
			},
		})
	}))
	defer srv.Close()
	cfAPIBase = srv.URL
	defer func() { cfAPIBase = "https://api.cloudflare.com/client/v4" }()

	got, err := cfResolveZone(context.Background(), "tok", "files.sub.example.com")
	if err != nil {
		t.Fatal(err)
	}
	if got.ID != "zone2" {
		t.Errorf("should match the longer zone sub.example.com, got %+v", got)
	}
}

func TestCfResolveZoneNoMatch(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"success": true, "errors": []any{},
			"result": []map[string]string{{"id": "zone1", "name": "unrelated.com"}},
		})
	}))
	defer srv.Close()
	cfAPIBase = srv.URL
	defer func() { cfAPIBase = "https://api.cloudflare.com/client/v4" }()

	_, err := cfResolveZone(context.Background(), "tok", "files.example.com")
	if err == nil {
		t.Fatal("expected an error, no zone covers this domain")
	}
}
```

- [ ] **Step 2: 跑测试确认失败**

Run: `GOTOOLCHAIN=go1.26.8 go test -run 'TestCfResolve' -v .`
Expected: FAIL，符号未定义

- [ ] **Step 3: 实现**

在 `cloudflare_api.go` 末尾追加：

```go
// 账号列表也是分页接口，必须用 cfList 翻完，否则账号多的用户会被漏掉
func cfListAccounts(ctx context.Context, token string) ([]cfAccount, error) {
	return cfList[cfAccount](ctx, pathAccounts(), token, nil)
}

// cfResolveAccount 返回要用的 account id：给了 hint 就直接用，不发请求；
// 没给就要求 token 只能访问恰好一个 account，多个就报错列出来让用户用 -account-id 选。
func cfResolveAccount(ctx context.Context, token, hint string) (string, error) {
	if hint != "" {
		return hint, nil
	}
	accounts, err := cfListAccounts(ctx, token)
	if err != nil {
		return "", err
	}
	switch len(accounts) {
	case 0:
		return "", fmt.Errorf("this token cannot access any Cloudflare account")
	case 1:
		return accounts[0].ID, nil
	default:
		names := make([]string, len(accounts))
		for i, a := range accounts {
			names[i] = fmt.Sprintf("%s (%s)", a.Name, a.ID)
		}
		return "", fmt.Errorf("this token can access multiple accounts, pick one with -account-id:\n  %s",
			strings.Join(names, "\n  "))
	}
}

// zone 列表默认每页只有 20 条，域名多的账号必须翻页才能找全，
// 不然 cfResolveZone 会误报"没有 zone 覆盖这个域名"
func cfListZones(ctx context.Context, token string) ([]cfZone, error) {
	return cfList[cfZone](ctx, pathZones(), token, nil)
}

// cfResolveZone 在 token 能访问的 zone 里找 domain 的最长后缀匹配。
// domain=files.sub.example.com，zone 列表里同时有 example.com 和 sub.example.com，
// 用更具体的 sub.example.com。
func cfResolveZone(ctx context.Context, token, domain string) (cfZone, error) {
	zones, err := cfListZones(ctx, token)
	if err != nil {
		return cfZone{}, err
	}
	var best cfZone
	for _, z := range zones {
		if domain != z.Name && !strings.HasSuffix(domain, "."+z.Name) {
			continue
		}
		if len(z.Name) > len(best.Name) {
			best = z
		}
	}
	if best.ID == "" {
		return cfZone{}, fmt.Errorf("no zone in this token's account covers %q; check the domain spelling or the token's Zone permissions", domain)
	}
	return best, nil
}
```

- [ ] **Step 4: 跑测试确认通过**

Run: `GOTOOLCHAIN=go1.26.8 go test -run 'TestCfResolve' -v .`
Expected: PASS（5 个测试全过）

- [ ] **Step 5: 跑全量测试**

Run: `GOTOOLCHAIN=go1.26.8 go vet ./... && GOTOOLCHAIN=go1.26.8 go test -count=1 ./...`
Expected: 全部 PASS

- [ ] **Step 6: Commit**

```bash
git add cloudflare_api.go cloudflare_api_test.go
git commit -m "Add Cloudflare account and zone resolution"
```

---

### Task 5: tunnel 查找 / 创建 / 取 token

**Files:**
- Modify: `cloudflare_api.go`
- Modify: `cloudflare_api_test.go`

**Interfaces:**
- Consumes: Task 3 的 `cfDo`、`cfTunnel`；`main.go` 里已有的 `tunnelInfo` 结构体
- **必须用 Task 3 铺好的地基**：list 类接口一律走 `cfList`（它会翻页，直接用 `cfDo`
  只能拿到第一页，域名/账号多的用户会被漏掉）；路径一律用 `pathXxx()` 函数，
  不要自己 `fmt.Sprintf` 拼；`CNAME`/`cloudflare`/`.cfargotunnel.com` 用
  `dnsRecordType`/`tunnelConfigSrc`/`tunnelDNSSuffix` 常量
- Produces: `cfFindTunnel(ctx, token, accountID, name) (*cfTunnel, error)`、
  `cfCreateTunnel(ctx, token, accountID, name) (cfTunnel, error)`、
  `cfGetTunnelToken(ctx, token, accountID, tunnelID) (*tunnelInfo, error)`

- [ ] **Step 1: 写失败测试**

在 `cloudflare_api_test.go` 末尾追加：

```go
func TestCfFindTunnelFound(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.URL.Query().Get("name"); got != "trynet-files-example-com" {
			t.Errorf("wrong name filter: %q", got)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"success": true, "errors": []any{},
			"result": []map[string]string{{"id": "tun1", "name": "trynet-files-example-com"}},
		})
	}))
	defer srv.Close()
	cfAPIBase = srv.URL
	defer func() { cfAPIBase = "https://api.cloudflare.com/client/v4" }()

	got, err := cfFindTunnel(context.Background(), "tok", "acct1", "trynet-files-example-com")
	if err != nil {
		t.Fatal(err)
	}
	if got == nil || got.ID != "tun1" {
		t.Errorf("got %+v", got)
	}
}

func TestCfFindTunnelNotFound(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"success": true, "errors": []any{}, "result": []map[string]string{},
		})
	}))
	defer srv.Close()
	cfAPIBase = srv.URL
	defer func() { cfAPIBase = "https://api.cloudflare.com/client/v4" }()

	got, err := cfFindTunnel(context.Background(), "tok", "acct1", "trynet-nope")
	if err != nil {
		t.Fatal(err)
	}
	if got != nil {
		t.Errorf("expected nil for not-found, got %+v", got)
	}
}

func TestCfGetTunnelToken(t *testing.T) {
	// 真实 token 是 base64({"a":accountTag,"t":tunnelID,"s":base64(secret)})
	rawToken := `eyJhIjoiYWNjb3VudC10YWciLCJ0IjoidHVubmVsLWlkIiwicyI6ImMyVmpjbVYwZG5jPSJ9`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"success": true, "errors": []any{}, "result": rawToken,
		})
	}))
	defer srv.Close()
	cfAPIBase = srv.URL
	defer func() { cfAPIBase = "https://api.cloudflare.com/client/v4" }()

	info, err := cfGetTunnelToken(context.Background(), "tok", "acct1", "tun1")
	if err != nil {
		t.Fatal(err)
	}
	if info.AccountTag != "account-tag" || info.ID != "tunnel-id" {
		t.Errorf("got %+v", info)
	}
	if string(info.Secret) != "secretvw" {
		t.Errorf("secret decoded wrong: %q", info.Secret)
	}
}
```

`rawToken` 是提前用脚本算好并且用 Go 跑过一遍确认解码正确的：内层 JSON 是
`{"a":"account-tag","t":"tunnel-id","s":"c2VjcmV0dnc="}`（`s` 字段的值是
base64("secretvw")，因为 `encoding/json` 对 `[]byte` 字段会自动做一次 base64 解码，
解出来才是 `info.Secret == "secretvw"`），整个 JSON 字符串再 base64 一次得到
`rawToken`。这一段直接照抄，不用自己重新算——已经验证过，别再手改。

- [ ] **Step 2: 跑测试确认失败**

Run: `GOTOOLCHAIN=go1.26.8 go test -run 'TestCfFindTunnel|TestCfGetTunnelToken' -v .`
Expected: FAIL，符号未定义

- [ ] **Step 3: 实现**

在 `cloudflare_api.go` 末尾追加：

```go
type cfCreateTunnelBody struct {
	Name      string `json:"name"`
	ConfigSrc string `json:"config_src"`
}

// cfFindTunnel 按名字查一个存量 tunnel，不存在返回 (nil, nil)，不当错误处理。
func cfFindTunnel(ctx context.Context, token, accountID, name string) (*cfTunnel, error) {
	q := url.Values{"name": {name}, "is_deleted": {"false"}}
	tunnels, err := cfList[cfTunnel](ctx, pathTunnels(accountID), token, q)
	if err != nil {
		return nil, err
	}
	for _, t := range tunnels {
		if t.Name == name {
			return &t, nil
		}
	}
	return nil, nil
}

// cfCreateTunnel 建一个"远程管理"的 tunnel（config_src=cloudflare），
// 不自己生成 tunnel_secret，交给 Cloudflare 管理——这样才能用下面 cfGetTunnelToken
// 随时重新要 token，不用在本地存密钥。
func cfCreateTunnel(ctx context.Context, token, accountID, name string) (cfTunnel, error) {
	body := cfCreateTunnelBody{Name: name, ConfigSrc: tunnelConfigSrc}
	return cfDo[cfTunnel](ctx, http.MethodPost, pathTunnels(accountID), token, body)
}

// cfGetTunnelToken 取一个 tunnel 的 connector token 并解成现有的 tunnelInfo 结构，
// 新建的、复用的都走这一个函数。这个接口对已存在的 tunnel 也能随时重新调用。
func cfGetTunnelToken(ctx context.Context, token, accountID, tunnelID string) (*tunnelInfo, error) {
	raw, err := cfDo[string](ctx, http.MethodGet, pathTunnelToken(accountID, tunnelID), token, nil)
	if err != nil {
		return nil, err
	}
	decoded, err := base64.StdEncoding.DecodeString(raw)
	if err != nil {
		return nil, fmt.Errorf("decode tunnel token: %w", err)
	}
	var t struct {
		AccountTag string `json:"a"`
		TunnelID   string `json:"t"`
		Secret     []byte `json:"s"`
	}
	if err := json.Unmarshal(decoded, &t); err != nil {
		return nil, fmt.Errorf("parse tunnel token: %w", err)
	}
	return &tunnelInfo{ID: t.TunnelID, AccountTag: t.AccountTag, Secret: t.Secret}, nil
}
```

`import` 里加 `"encoding/base64"` 和 `"net/url"`。

- [ ] **Step 4: 跑测试确认通过**

Run: `GOTOOLCHAIN=go1.26.8 go test -run 'TestCfFindTunnel|TestCfGetTunnelToken' -v .`
Expected: PASS（3 个测试全过）

- [ ] **Step 5: 跑全量测试**

Run: `GOTOOLCHAIN=go1.26.8 go vet ./... && GOTOOLCHAIN=go1.26.8 go test -count=1 ./...`
Expected: 全部 PASS

- [ ] **Step 6: Commit**

```bash
git add cloudflare_api.go cloudflare_api_test.go
git commit -m "Add tunnel find/create and connector token fetch"
```

---

### Task 6: DNS 记录查找 / 创建 / 删除，tunnel 删除

**Files:**
- Modify: `cloudflare_api.go`
- Modify: `cloudflare_api_test.go`

**Interfaces:**
- Consumes: Task 3 的 `cfDo`、`cfDNSRecord`
- **必须用 Task 3 铺好的地基**：list 类接口一律走 `cfList`（它会翻页，直接用 `cfDo`
  只能拿到第一页，域名/账号多的用户会被漏掉）；路径一律用 `pathXxx()` 函数，
  不要自己 `fmt.Sprintf` 拼；`CNAME`/`cloudflare`/`.cfargotunnel.com` 用
  `dnsRecordType`/`tunnelConfigSrc`/`tunnelDNSSuffix` 常量
- Produces: `cfFindDNSRecord(ctx, token, zoneID, name) (*cfDNSRecord, error)`、
  `cfCreateDNSRecord(ctx, token, zoneID, domain, target) (cfDNSRecord, error)`、
  `cfDeleteDNSRecord(ctx, token, zoneID, recordID) error`、
  `cfDeleteTunnel(ctx, token, accountID, tunnelID) error`

- [ ] **Step 1: 写失败测试**

在 `cloudflare_api_test.go` 末尾追加：

```go
func TestCfFindDNSRecord(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.URL.Query().Get("type"); got != "CNAME" {
			t.Errorf("wrong type filter: %q", got)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"success": true, "errors": []any{},
			"result": []map[string]any{
				{"id": "rec1", "name": "files.example.com", "type": "CNAME",
					"content": "tun1.cfargotunnel.com", "proxied": true},
			},
		})
	}))
	defer srv.Close()
	cfAPIBase = srv.URL
	defer func() { cfAPIBase = "https://api.cloudflare.com/client/v4" }()

	got, err := cfFindDNSRecord(context.Background(), "tok", "zone1", "files.example.com")
	if err != nil {
		t.Fatal(err)
	}
	if got == nil || got.Content != "tun1.cfargotunnel.com" {
		t.Errorf("got %+v", got)
	}
}

func TestCfCreateDNSRecordIsProxied(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body cfCreateDNSBody
		_ = json.NewDecoder(r.Body).Decode(&body)
		if !body.Proxied {
			t.Error("dns record must be proxied (orange cloud), otherwise the tunnel doesn't work")
		}
		if body.Type != "CNAME" {
			t.Errorf("wrong type: %q", body.Type)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"success": true, "errors": []any{},
			"result": map[string]any{"id": "rec1", "name": body.Name, "type": body.Type,
				"content": body.Content, "proxied": body.Proxied},
		})
	}))
	defer srv.Close()
	cfAPIBase = srv.URL
	defer func() { cfAPIBase = "https://api.cloudflare.com/client/v4" }()

	got, err := cfCreateDNSRecord(context.Background(), "tok", "zone1", "files.example.com", "tun1.cfargotunnel.com")
	if err != nil {
		t.Fatal(err)
	}
	if got.ID != "rec1" {
		t.Errorf("got %+v", got)
	}
}

func TestCfDeleteDNSRecordAndTunnel(t *testing.T) {
	var gotMethod, gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod, gotPath = r.Method, r.URL.Path
		_ = json.NewEncoder(w).Encode(map[string]any{"success": true, "errors": []any{}, "result": map[string]any{}})
	}))
	defer srv.Close()
	cfAPIBase = srv.URL
	defer func() { cfAPIBase = "https://api.cloudflare.com/client/v4" }()

	if err := cfDeleteDNSRecord(context.Background(), "tok", "zone1", "rec1"); err != nil {
		t.Fatal(err)
	}
	if gotMethod != http.MethodDelete || gotPath != "/zones/zone1/dns_records/rec1" {
		t.Errorf("got %s %s", gotMethod, gotPath)
	}

	if err := cfDeleteTunnel(context.Background(), "tok", "acct1", "tun1"); err != nil {
		t.Fatal(err)
	}
	if gotMethod != http.MethodDelete || gotPath != "/accounts/acct1/cfd_tunnel/tun1" {
		t.Errorf("got %s %s", gotMethod, gotPath)
	}
}
```

- [ ] **Step 2: 跑测试确认失败**

Run: `GOTOOLCHAIN=go1.26.8 go test -run 'TestCfFindDNSRecord|TestCfCreateDNSRecord|TestCfDelete' -v .`
Expected: FAIL，符号未定义

- [ ] **Step 3: 实现**

在 `cloudflare_api.go` 末尾追加：

```go
type cfCreateDNSBody struct {
	Type    string `json:"type"`
	Name    string `json:"name"`
	Content string `json:"content"`
	Proxied bool   `json:"proxied"`
}

func cfFindDNSRecord(ctx context.Context, token, zoneID, name string) (*cfDNSRecord, error) {
	q := url.Values{"type": {dnsRecordType}, "name": {name}}
	records, err := cfList[cfDNSRecord](ctx, pathDNSRecords(zoneID), token, q)
	if err != nil {
		return nil, err
	}
	if len(records) == 0 {
		return nil, nil
	}
	return &records[0], nil
}

// cfCreateDNSRecord 建一条指向 tunnel 的 CNAME。必须 proxied=true（橙云）——
// 灰云的话这条记录只是普通 DNS 解析，压根连不到 Cloudflare 边缘的隧道路由上。
func cfCreateDNSRecord(ctx context.Context, token, zoneID, domain, target string) (cfDNSRecord, error) {
	body := cfCreateDNSBody{Type: dnsRecordType, Name: domain, Content: target, Proxied: true}
	return cfDo[cfDNSRecord](ctx, http.MethodPost, pathDNSRecords(zoneID), token, body)
}

func cfDeleteDNSRecord(ctx context.Context, token, zoneID, recordID string) error {
	_, err := cfDo[map[string]any](ctx, http.MethodDelete, pathDNSRecord(zoneID, recordID), token, nil)
	return err
}

func cfDeleteTunnel(ctx context.Context, token, accountID, tunnelID string) error {
	_, err := cfDo[map[string]any](ctx, http.MethodDelete, pathTunnel(accountID, tunnelID), token, nil)
	return err
}
```

- [ ] **Step 4: 跑测试确认通过**

Run: `GOTOOLCHAIN=go1.26.8 go test -run 'TestCfFindDNSRecord|TestCfCreateDNSRecord|TestCfDelete' -v .`
Expected: PASS（3 个测试全过）

- [ ] **Step 5: 跑全量测试**

Run: `GOTOOLCHAIN=go1.26.8 go vet ./... && GOTOOLCHAIN=go1.26.8 go test -count=1 ./...`
Expected: 全部 PASS

- [ ] **Step 6: Commit**

```bash
git add cloudflare_api.go cloudflare_api_test.go
git commit -m "Add DNS record CRUD and tunnel delete"
```

---

### Task 7: setupNamedTunnel 编排 + namedTunnelHandle（含 teardown）

**Files:**
- Modify: `cloudflare_api.go`
- Modify: `cloudflare_api_test.go`

**Interfaces:**
- Consumes: Task 4/5/6 的全部函数；`main.go` 的 `tunnelInfo`
- Produces: `setupNamedTunnel(ctx, token, accountHint, domain string) (*tunnelInfo, *namedTunnelHandle, error)`、
  `(*namedTunnelHandle).teardown() error`、`(*namedTunnelHandle).refreshToken(ctx) (*tunnelInfo, error)`
  （Task 8 的 `main.go` 直接用这三个）

- [ ] **Step 1: 写失败测试**

在 `cloudflare_api_test.go` 末尾追加。这里用一个小 helper 起一个能同时响应
accounts/zones/tunnel/dns 这几条路径的 fake server，模拟"全新创建"和"全部复用"
两条路径：

```go
// newFakeCloudflareServer 起一个假的 Cloudflare API，tunnelExists/dnsExists
// 控制它是走"查到已有资源直接复用"还是"什么都没有，从头创建"这条路径。
func newFakeCloudflareServer(t *testing.T, tunnelExists, dnsExists bool) *httptest.Server {
	t.Helper()
	rawToken := `eyJhIjoiYWNjb3VudC10YWciLCJ0IjoidHVubmVsLWlkIiwicyI6ImMyVmpjbVYwZG5jPSJ9`

	mux := http.NewServeMux()
	mux.HandleFunc("/accounts", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"success": true, "errors": []any{},
			"result": []map[string]string{{"id": "acct1", "name": "Test"}}})
	})
	mux.HandleFunc("/zones", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"success": true, "errors": []any{},
			"result": []map[string]string{{"id": "zone1", "name": "example.com"}}})
	})
	mux.HandleFunc("/accounts/acct1/cfd_tunnel", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			_ = json.NewEncoder(w).Encode(map[string]any{"success": true, "errors": []any{},
				"result": map[string]string{"id": "tunnel-id", "name": "trynet-files-example-com"}})
			return
		}
		result := []map[string]string{}
		if tunnelExists {
			result = []map[string]string{{"id": "tunnel-id", "name": "trynet-files-example-com"}}
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"success": true, "errors": []any{}, "result": result})
	})
	mux.HandleFunc("/accounts/acct1/cfd_tunnel/tunnel-id/token", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"success": true, "errors": []any{}, "result": rawToken})
	})
	mux.HandleFunc("/zones/zone1/dns_records", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			var body cfCreateDNSBody
			_ = json.NewDecoder(r.Body).Decode(&body)
			_ = json.NewEncoder(w).Encode(map[string]any{"success": true, "errors": []any{},
				"result": map[string]any{"id": "rec1", "name": body.Name, "type": body.Type,
					"content": body.Content, "proxied": body.Proxied}})
			return
		}
		result := []map[string]any{}
		if dnsExists {
			result = []map[string]any{{"id": "rec1", "name": "files.example.com", "type": "CNAME",
				"content": "tunnel-id.cfargotunnel.com", "proxied": true}}
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"success": true, "errors": []any{}, "result": result})
	})
	mux.HandleFunc("/accounts/acct1/cfd_tunnel/tunnel-id", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"success": true, "errors": []any{}, "result": map[string]any{}})
	})
	mux.HandleFunc("/zones/zone1/dns_records/rec1", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"success": true, "errors": []any{}, "result": map[string]any{}})
	})

	return httptest.NewServer(mux)
}

func TestSetupNamedTunnelCreatesEverything(t *testing.T) {
	srv := newFakeCloudflareServer(t, false, false)
	defer srv.Close()
	cfAPIBase = srv.URL
	defer func() { cfAPIBase = "https://api.cloudflare.com/client/v4" }()

	info, handle, err := setupNamedTunnel(context.Background(), "tok", "", "files.example.com")
	if err != nil {
		t.Fatal(err)
	}
	if info.Hostname != "files.example.com" || info.ID != "tunnel-id" {
		t.Errorf("got %+v", info)
	}
	if handle.dnsRecordID == "" {
		t.Error("newly created dns record id should be recorded for teardown")
	}
}

func TestSetupNamedTunnelReusesEverything(t *testing.T) {
	srv := newFakeCloudflareServer(t, true, true)
	defer srv.Close()
	cfAPIBase = srv.URL
	defer func() { cfAPIBase = "https://api.cloudflare.com/client/v4" }()

	info, handle, err := setupNamedTunnel(context.Background(), "tok", "", "files.example.com")
	if err != nil {
		t.Fatal(err)
	}
	if info.ID != "tunnel-id" {
		t.Errorf("got %+v", info)
	}
	if handle.dnsRecordID != "" {
		t.Error("reused dns record should not be marked for teardown deletion")
	}
}

func TestSetupNamedTunnelConflictingDNS(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/accounts", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"success": true, "errors": []any{},
			"result": []map[string]string{{"id": "acct1", "name": "Test"}}})
	})
	mux.HandleFunc("/zones", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"success": true, "errors": []any{},
			"result": []map[string]string{{"id": "zone1", "name": "example.com"}}})
	})
	mux.HandleFunc("/accounts/acct1/cfd_tunnel", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"success": true, "errors": []any{},
			"result": []map[string]string{{"id": "tunnel-id", "name": "trynet-files-example-com"}}})
	})
	mux.HandleFunc("/accounts/acct1/cfd_tunnel/tunnel-id/token", func(w http.ResponseWriter, r *http.Request) {
		rawToken := `eyJhIjoiYWNjb3VudC10YWciLCJ0IjoidHVubmVsLWlkIiwicyI6ImMyVmpjbVYwZG5jPSJ9`
		_ = json.NewEncoder(w).Encode(map[string]any{"success": true, "errors": []any{}, "result": rawToken})
	})
	mux.HandleFunc("/zones/zone1/dns_records", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"success": true, "errors": []any{},
			"result": []map[string]any{{"id": "rec1", "name": "files.example.com", "type": "CNAME",
				"content": "somewhere-else.example.net", "proxied": false}}})
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	cfAPIBase = srv.URL
	defer func() { cfAPIBase = "https://api.cloudflare.com/client/v4" }()

	_, _, err := setupNamedTunnel(context.Background(), "tok", "", "files.example.com")
	if err == nil {
		t.Fatal("expected an error, dns record points elsewhere and should not be overwritten")
	}
	if !strings.Contains(err.Error(), "somewhere-else.example.net") {
		t.Errorf("error should mention what it currently points to, got: %v", err)
	}
}
```

- [ ] **Step 2: 跑测试确认失败**

Run: `GOTOOLCHAIN=go1.26.8 go test -run 'TestSetupNamedTunnel' -v .`
Expected: FAIL，符号未定义

- [ ] **Step 3: 实现**

在 `cloudflare_api.go` 末尾追加：

```go
// namedTunnelHandle 记着 setupNamedTunnel 建好/复用的资源 id，
// 用来支撑 -teardown（精确删除）和注册被拒绝时的 token 刷新。
type namedTunnelHandle struct {
	token       string
	accountID   string
	zoneID      string
	tunnelID    string
	domain      string
	dnsRecordID string // 只有这次自己新建的记录才有值；发现已存在的记录不会被删
}

// setupNamedTunnel 编排"解析身份 -> 找/建 tunnel -> 取 token -> 对好 DNS"整条链路。
// 返回的 info 可以直接喂给现有的 serve()/run()。
func setupNamedTunnel(ctx context.Context, token, accountHint, domain string) (*tunnelInfo, *namedTunnelHandle, error) {
	accountID, err := cfResolveAccount(ctx, token, accountHint)
	if err != nil {
		return nil, nil, fmt.Errorf("resolve account: %w", err)
	}

	zone, err := cfResolveZone(ctx, token, domain)
	if err != nil {
		return nil, nil, fmt.Errorf("resolve zone: %w", err)
	}

	name := sanitizeTunnelName(domain)
	existing, err := cfFindTunnel(ctx, token, accountID, name)
	if err != nil {
		return nil, nil, fmt.Errorf("find tunnel: %w", err)
	}
	tunnelID := ""
	if existing != nil {
		tunnelID = existing.ID
	} else {
		created, err := cfCreateTunnel(ctx, token, accountID, name)
		if err != nil {
			return nil, nil, fmt.Errorf("create tunnel: %w", err)
		}
		tunnelID = created.ID
	}

	info, err := cfGetTunnelToken(ctx, token, accountID, tunnelID)
	if err != nil {
		return nil, nil, fmt.Errorf("get tunnel token: %w", err)
	}
	info.Hostname = domain

	handle := &namedTunnelHandle{token: token, accountID: accountID, zoneID: zone.ID, tunnelID: tunnelID, domain: domain}

	target := tunnelID + tunnelDNSSuffix
	record, err := cfFindDNSRecord(ctx, token, zone.ID, domain)
	if err != nil {
		return nil, nil, fmt.Errorf("find dns record: %w", err)
	}
	switch {
	case record != nil && record.Content == target:
		// 已经指对了，什么都不用做
	case record != nil:
		return nil, nil, fmt.Errorf("dns record for %s already points to %q, not %q; fix it manually or use a different domain",
			domain, record.Content, target)
	default:
		created, err := cfCreateDNSRecord(ctx, token, zone.ID, domain, target)
		if err != nil {
			return nil, nil, fmt.Errorf("create dns record: %w", err)
		}
		handle.dnsRecordID = created.ID
	}

	return info, handle, nil
}

// refreshToken 只重新取一次 connector token，不重复走建 tunnel/建 DNS 那一套——
// 那些资源已经在了。run() 在注册被拒绝时调这个。
func (h *namedTunnelHandle) refreshToken(ctx context.Context) (*tunnelInfo, error) {
	info, err := cfGetTunnelToken(ctx, h.token, h.accountID, h.tunnelID)
	if err != nil {
		return nil, err
	}
	info.Hostname = h.domain
	return info, nil
}

// teardown 删掉这次自动创建/复用的云端资源：DNS 记录（只删自己新建的那种）和 tunnel。
// tunnel 删除紧跟在连接刚断开之后调用，边缘那边有时候还没反应过来这条连接已经关了，
// 会报"active connections"之类的错误，所以这里做几次重试，隔几秒再试。
func (h *namedTunnelHandle) teardown() error {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	var errs []error
	if h.dnsRecordID != "" {
		if err := cfDeleteDNSRecord(ctx, h.token, h.zoneID, h.dnsRecordID); err != nil {
			errs = append(errs, fmt.Errorf("delete dns record: %w", err))
		}
	}

	var lastErr error
	for attempt := 0; attempt < 3; attempt++ {
		if lastErr = cfDeleteTunnel(ctx, h.token, h.accountID, h.tunnelID); lastErr == nil {
			break
		}
		if !sleepCtx(ctx, 2*time.Second) {
			break
		}
	}
	if lastErr != nil {
		errs = append(errs, fmt.Errorf("delete tunnel: %w", lastErr))
	}

	return errors.Join(errs...)
}
```

`import` 里加 `"errors"` 和 `"time"`（`sleepCtx` 是 Task 里已有的 `main.go` 函数，
同一个 package 里直接能用）。

- [ ] **Step 4: 跑测试确认通过**

Run: `GOTOOLCHAIN=go1.26.8 go test -run 'TestSetupNamedTunnel' -v .`
Expected: PASS（3 个测试全过）

- [ ] **Step 5: 跑全量测试**

Run: `GOTOOLCHAIN=go1.26.8 go vet ./... && GOTOOLCHAIN=go1.26.8 go build ./... && GOTOOLCHAIN=go1.26.8 go test -count=1 ./...`
Expected: 全部 PASS

- [ ] **Step 6: Commit**

```bash
git add cloudflare_api.go cloudflare_api_test.go
git commit -m "Add setupNamedTunnel orchestration with reuse and teardown"
```

---

### Task 8: main.go 接入固定域名模式

**Files:**
- Create: `tunnel.go`（从 main.go 拆出来，见 Step 0）
- Modify: `main.go`

**Interfaces:**
- Consumes: Task 1 的 `envOr`/`envOrInt`/`envOrBool`、Task 2 的
  `printTokenInfo(token, source string)`、Task 7 的 `setupNamedTunnel`/`namedTunnelHandle`
- Produces: `run()` 新签名 `run(ctx, info, origin, refresh)`（供 Task 9 端到端验证时调用）

- [ ] **Step 0: 先把 main.go 拆开（它已经 480 行，再加就破 500 行上限了）**

新建 `tunnel.go`，把 `main.go` 里下面这些**原样剪切**过去（一个字都不要改，
纯粹是文件搬家）：

- `serveResult` 结构体
- `serve` 函数
- `tunnel` 结构体及其全部方法（`setResult`、`gracefulShutdown`、`ServeHTTP`、
  `rewrite`、`serveControlStream`、`connOptions`）
- `addrIP` 函数
- `streamRWC` 结构体及其方法
- `modifyResponse`、`isControlResponseHeader`、`serializeHeaders` 函数
- `headerEncoding` 变量

`tunnel.go` 开头写：

```go
// tunnel.go 管一条边缘连接的生命周期：在已建立的 TLS 连接上跑 HTTP/2 服务端、
// 用控制流完成注册、把边缘转进来的请求反代到本地。
// main.go 负责"拿到凭据、决定连哪里"，这里负责"连上之后怎么办"。
package main
```

`import` 按两边各自实际用到的包重新整理（`goimports` 或手动删掉 main.go 里
搬走之后不再使用的 import）。

搬完先确认没搬坏：

Run: `GOTOOLCHAIN=go1.26.8 go build ./... && GOTOOLCHAIN=go1.26.8 go test -count=1 ./...`
Expected: 全部 PASS，行为跟搬之前完全一样（这一步只是移动代码，不改任何逻辑）

然后确认两个文件都在 500 行以内：

Run: `wc -l main.go tunnel.go`

- [ ] **Step 1: 给 run() 加 refresh 参数**

把 `main.go` 里的：

```go
func run(ctx context.Context, info *tunnelInfo, origin string) {
	bo := newBackoff()
	for i := 0; ctx.Err() == nil; i++ {
		host := edgeHosts[i%len(edgeHosts)]
		result := serve(ctx, info, origin, host)
		if ctx.Err() != nil {
			return
		}

		if result.rejected {
			log.Printf("tunnel registration rejected: %v; requesting a new tunnel", result.err)
			if fresh, err := requestQuickTunnel(); err != nil {
				log.Printf("quick tunnel request: %v", err)
			} else {
				info = fresh
			}
		}
```

改成：

```go
func run(ctx context.Context, info *tunnelInfo, origin string, refresh func() (*tunnelInfo, error)) {
	bo := newBackoff()
	for i := 0; ctx.Err() == nil; i++ {
		host := edgeHosts[i%len(edgeHosts)]
		result := serve(ctx, info, origin, host)
		if ctx.Err() != nil {
			return
		}

		if result.rejected {
			log.Printf("tunnel registration rejected: %v; requesting a new tunnel", result.err)
			if fresh, err := refresh(); err != nil {
				log.Printf("refresh tunnel: %v", err)
			} else {
				info = fresh
			}
		}
```

（其余 `run()` 函数体不变。）

- [ ] **Step 2: 新增四个 flag，并把取 tunnelInfo 的逻辑接上**

先在 `main()` 的 `flag.Parse()` 之前、`-dir` 那行之后，补上四个新参数：

```go
	domain := flag.String("domain", envOr("TRYNET_DOMAIN", ""), "fixed domain to publish on, via a Cloudflare named tunnel")
	apiToken := flag.String("api-token", envOr("CLOUDFLARE_API_TOKEN", ""), "Cloudflare API token (required with -domain)")
	accountID := flag.String("account-id", envOr("CLOUDFLARE_ACCOUNT_ID", ""), "Cloudflare account id (only needed if the token can access more than one account)")
	teardown := flag.Bool("teardown", envOrBool("TRYNET_TEARDOWN", false), "delete the auto-created tunnel and dns record on exit (only with -domain)")
```

然后把已有的：

```go
	info, err := requestQuickTunnel()
	if err != nil {
		log.Fatalf("quick tunnel request: %v", err)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	run(ctx, info, origin)
}
```

一并替换成：

```go
	var info *tunnelInfo
	var refresh func() (*tunnelInfo, error)
	var teardownFn func() error

	if *domain != "" {
		printTokenInfo(*apiToken, tokenSource())
		if *apiToken == "" {
			log.Fatal("no Cloudflare API token: set CLOUDFLARE_API_TOKEN or pass -api-token")
		}

		setupCtx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		setupInfo, handle, err := setupNamedTunnel(setupCtx, *apiToken, *accountID, *domain)
		cancel()
		if err != nil {
			log.Fatalf("named tunnel setup: %v", err)
		}
		info = setupInfo
		refresh = func() (*tunnelInfo, error) {
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			return handle.refreshToken(ctx)
		}
		if *teardown {
			teardownFn = handle.teardown
		}
	} else {
		fetched, err := requestQuickTunnel()
		if err != nil {
			log.Fatalf("quick tunnel request: %v", err)
		}
		info = fetched
		refresh = requestQuickTunnel
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	run(ctx, info, origin, refresh)

	if teardownFn != nil {
		log.Print("tearing down the auto-created tunnel and dns record")
		if err := teardownFn(); err != nil {
			log.Printf("teardown: %v", err)
		} else {
			log.Print("teardown: done")
		}
	}
}
```

- [ ] **Step 2.5: 加 tokenSource() 帮 banner 说出真实来源**

`printTokenInfo` 需要知道 token 到底是从命令行 flag 来的还是从环境变量来的。
因为我们把环境变量的值当成了 flag 的默认值，parse 完之后光看值分不出来源——
得用 `flag.Visit`，它只遍历命令行上**显式给过**的 flag。

在 `main.go` 里 `envOrBool` 下面加：

```go
// tokenSource 说明 API token 实际是从哪来的，给启动横幅显示用。
// 环境变量的值是作为 flag 默认值注入的，parse 完就分不出来源了，
// 只能靠 flag.Visit——它只遍历命令行上显式给过的 flag。
func tokenSource() string {
	source := "CLOUDFLARE_API_TOKEN"
	flag.Visit(func(f *flag.Flag) {
		if f.Name == "api-token" {
			source = "-api-token"
		}
	})
	return source
}
```

- [ ] **Step 3: 跑 vet 和 build 确认能编译**

Run: `GOTOOLCHAIN=go1.26.8 go vet ./... && GOTOOLCHAIN=go1.26.8 go build ./...`
Expected: 无报错（`run()` 调用点已经全部改成 4 个参数）

说明：`run()`/`serve()` 本身是网络编排代码，一直以来（包括之前几轮健壮性修复）都是
靠真实网络连接做端到端验证，不做重网络 mock——这次的改动只是多穿一个 `refresh`
参数，不改内部状态机，遵循同样的验证方式，放在 Task 9 里用真实 quick tunnel 跑一遍
回归。

- [ ] **Step 4: 跑现有全量测试确认没有回归（quick tunnel 路径的测试应该照常通过）**

Run: `GOTOOLCHAIN=go1.26.8 go test -count=1 ./...`
Expected: 全部 PASS

- [ ] **Step 5: Commit**

```bash
git add main.go
git commit -m "Wire named-tunnel mode into main and add credential refresh to run()"
```

---

### Task 9: 收尾 —— 全量回归、体积重建、文档进度、用户验证提示

**Files:**
- Modify: `plans/plan_NamedTunnel.md`（勾掉进度）
- 无新代码文件

- [ ] **Step 1: 全量格式/vet/build/test**

```bash
cd C:/Users/blake/tmp/trynet
GOTOOLCHAIN=go1.26.8 gofmt -l .
GOTOOLCHAIN=go1.26.8 go vet ./...
GOTOOLCHAIN=go1.26.8 go build ./...
GOTOOLCHAIN=go1.26.8 go test -count=1 -v ./...
```

Expected: `gofmt -l` 无输出；其余全部 PASS

- [ ] **Step 2: quick tunnel 路径回归（确认没改坏原有功能）**

```bash
HTTPS_PROXY=socks5://<可用代理> ./trynet.exe -dir ./testdata/public
```

用另一个终端 `curl` 那个 `https://xxx.trycloudflare.com/hello.txt`，应该正常拿到内容，
跟之前几轮验证一样。

- [ ] **Step 3: 检查所有源文件行数，确认没有超过 500 行**

```bash
wc -l *.go
```

Expected: 每个文件都 < 500 行。如果 `cloudflare_api.go` 超了，把 DNS 相关函数
拆到 `cloudflare_dns.go` 里（同一个 `package main`，纯粹是文件层面的拆分）。

- [ ] **Step 4: 重建正式产物（延用现有的 -s -w + UPX 流程）**

```bash
rm -f trynet.exe trynet-raw.exe
GOTOOLCHAIN=go1.26.8 go build -trimpath -ldflags="-s -w" -o trynet-raw.exe .
upx --best -o trynet.exe trynet-raw.exe
```

- [ ] **Step 5: 更新 plan_NamedTunnel.md 的进度勾选**

把本文件第 9 节的进度改成：

```markdown
## 9. 进度

- [x] brainstorming 完成，设计定稿
- [x] 用户 review 本 spec
- [x] 转 writing-plans 细化实现步骤（见第 10 节）
- [x] 编码（Task 1-8 全部完成）
- [x] 单元测试（fake server）全绿
- [ ] 用户提供真实账号做一次端到端验证
```

- [ ] **Step 6: plans/ 不用提交**

用户的全局 gitignore 里配了 `**/plans/`，计划文档是故意不纳入版本控制的。
改完进度勾选就行，不要用 `git add -f` 强行提交。

- [ ] **Step 7: 提示用户做真实验证**

这一步没有代码要写，只是提醒：`setupNamedTunnel`/`cfGetTunnelToken`/DNS 相关的
真实网络行为（真实 Cloudflare API 返回的字段是否跟猜测的完全一致、真实账号的
权限报错文案）只能靠 fake server 覆盖到"我们以为 API 长什么样"，没法覆盖到
"API 真的长这样"——需要用户提供一个真实 Cloudflare 账号的 `CLOUDFLARE_API_TOKEN`
和一个已经加入该账号的域名，跑一次：

```bash
CLOUDFLARE_API_TOKEN=xxx trynet -domain files.<你的域名> -dir ./testdata/public
```

确认：banner 下面 token 那行显示正确来源、`tunnel ready` 打印的是固定域名而不是
随机域名、浏览器/`curl` 访问这个固定域名能拿到内容、Ctrl+C 能正常优雅退出、
（如果测了 `-teardown`）Cloudflare 后台的 tunnel 和 DNS 记录确实被删掉了。

---

## Self-Review（对照 spec 检查完的结论）

1. **spec 覆盖度**：spec 第 1-8 节列的每一条决策都能对应到上面的任务——身份自动解析
   （Task 4）、token 只走 flag/env（Task 1）、tunnel/DNS 复用与冲突处理（Task 7）、
   `-teardown`（Task 7/8）、不引入 cobra/TUI（体现在没有对应任务，维持 `flag` 标准库）、
   全参数 env-aware（Task 1）、banner 脱敏回显（Task 2）。没有遗漏项。
2. **占位符扫描**：全文没有 TBD/"后面再补"/"加适当的错误处理"这类空话，每一步都是
   完整可运行的代码。
3. **类型一致性**：`tunnelInfo`（`main.go` 已有）、`cfTunnel`/`cfZone`/`cfAccount`/
   `cfDNSRecord`（Task 3 定义）、`namedTunnelHandle`（Task 7 定义）在后续任务里全程
   同名同签名使用，没有出现"前面叫 A 后面叫 B"的情况。`run()` 的新签名
   `run(ctx, info, origin, refresh)` 在 Task 8 和 Task 9 里一致。
4. **范围检查**：这是一个单一子系统（固定域名模式），9 个任务都在为同一个可工作的
   交付物服务，不需要再拆分成多个 plan。

## Execution Handoff

Plan 已经写进 `plans/plan_NamedTunnel.md`（第 10 节起）。两种执行方式：

**1. Subagent-Driven（推荐）**——每个任务派一个新 subagent 去做，任务之间我来 review，
迭代快，出问题只影响单个任务。

**2. Inline Execution**——在当前会话里按 executing-plans 跑，批量执行、设检查点。

选哪个？
