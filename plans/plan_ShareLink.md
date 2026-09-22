# plan_ShareLink —— 单文件临时分享链接 + `-public` 改名

## 背景

会话 cookie 上线之后，`curl -u admin:xxx` 这条路彻底关死了，脚本和"把一个文件
发给别人"都没了出路。

第一版方案是让 curl 带上那个 8 位登录密码（header 或 `?key=`）。**作废**，原因是
它把授权粒度搞错了：登录密码是整个分享目录的钥匙，为了递一个文件出去就把全部内容
的访问权交出去，代价完全不成比例。而且密码是常驻的，发出去就收不回来。

改成**每个文件单独签发、会过期的下载 token**。

## 1. 授权模型

三条路径，权限范围依次收窄：

| 路径 | 谁用 | 范围 | 生命周期 |
|---|---|---|---|
| 会话 cookie | 浏览器 | 全部 `/data`，读 + 写 | 12h 滑动过期 |
| 分享 token | 收链接的人 / curl | **单个文件，只读** | 1h，固定不续期 |
| `-public` | 所有人 | 全部 `/data` | 进程存活期间 |

关键性质：

- token 与**签发时那一个路径**绑定。拿 `/data/a.pdf` 的 token 去请求
  `/data/b.pdf` 一律 401，不是"能访问所有文件"的通行证。
- 只对 `GET`/`HEAD` 有效。**上传走不通**——带 token 发 POST 是 401，
  分享链接永远不能变成写入口。
- 一小时固定过期，**不滑动续期**。滑动续期用在会话上是为了"人还在用就别踢他"；
  用在分享链接上等于"只要有人一直下载就永不过期"，恰好是它该避免的。
- 有效期内可重复使用。单次有效会挂在断点续传和失败重试上，实际不可用。
- 存在内存里，进程重启全部失效。这符合"临时"的定位，不做持久化。

## 2. 签发接口

```
POST /auth/share      要求有效会话 cookie（分享 token 自己签不出新 token）
     {"path": "/data/report.pdf"}
  → {"url": "/data/report.pdf?t=<token>", "expiresIn": 3600}
```

服务端在签发前拒绝三种路径，宁可签不出来也不签出一个越界的：

1. **目录** —— 用户要分享的是文件。给目录签 token 会泄漏一份文件名清单
   （里面的文件路径不同、token 对不上，下不动，但名字已经漏了）。
2. **被内容过滤挡掉的路径** —— `.env`、`id_rsa` 这些。不拦的话会签出一个
   永远 404 的 token，用户以为分享成功了，是更糟的结果。
3. **分享目录之外的路径** —— 复用 `resolveRequestPath` 那套 `filepath.Rel`
   包含性判断，不自己再写一遍。

token 是 16 字节 `crypto/rand`，base64url 编码 22 个字符。128 位随机，够了；
会话那边的 32 字节在这里是浪费——这串东西要塞进 URL 里给人复制。

## 3. 中间件

`requireAuth` 多认一条路：

```go
// 会话 cookie 命中 → 放行
// 否则，GET/HEAD 且 ?t= 的 token 与本次请求路径绑定 → 放行
// 否则 401（照旧不带 WWW-Authenticate）
```

`shares` 传 nil 表示该路由不接受分享 token，`/auth/share` 自己就走这条。

顺序不变：`requireAuth` 在最外层，内容过滤在里面。所以就算 token 签发时的检查
被绕过，过滤这道网还在。

## 4. `-no-auth` → `-public`

`-no-auth` 是否定式布尔，代码里全是 `if !*noAuth` 的双重否定。`-public`
描述状态而不是"关掉某个东西"。

- flag `-public`，环境变量 `TRYNET_PUBLIC`
- `fileServerConfig.noAuth` → `.public`（用到的地方一并反转）
- 旧名字刚加不久、没有用户，直接换掉，不留别名
- `-public` 时 `/auth/share` 不挂载，页面上的分享按钮也不渲染——
  东西都公开了，再签一个受限 token 没有意义

### `-public` + `-upload`

保持放行（用户明确要的是"无需验证"，不是"只读"），但 banner 打一行显眼警告：
公网可写的目录，任何人都能往里塞东西。

## 5. 界面

每个文件行在下载箭头旁边多一个链接图标。点一下：

1. `POST /auth/share` 拿到 URL
2. 底部弹一条 toast，里面是一个只读 input，内容是完整绝对 URL，自动全选
3. 同时静默尝试 `navigator.clipboard.writeText`

**只走这一条路径**，不分"复制成功/失败"两个分支：无论剪贴板 API 成不成，
用户都看得见链接、都能 Ctrl+C。少一个分支少一类 bug。

文案：`Share link · expires in 1 hour`

目录行没有分享按钮（签发接口本来就会拒绝目录，按钮不该出现）。

## 6. 测试

服务端：

- token 只对签发路径有效：`/data/a.txt` 的 token 请求 `/data/b.txt` → 401
- 过期 token → 401
- token 带 POST 上传 → 401（分享链接不能变写入口）
- token 不续期：快到期时用一次，仍按原时间过期
- 签发拒绝：目录、敏感文件名、目录外路径
- 无会话时 `POST /auth/share` → 401
- 分享 token 签不出新 token（拿 token 去 POST /auth/share → 401）
- `-public`：`/auth/share` 不存在；`/data` 无凭据可读

改名：

- `-public` flag 生效；`TRYNET_PUBLIC=1` 生效；flag 优先于环境变量

每条改完都要做变异验证：先把实现改坏，确认测试真的变红，再还原。

## 7. 文件

- 新增 `share.go` —— shareStore + 签发 handler
- 改 `session.go` —— `requireAuth` 多一个 shares 参数
- 改 `fileserver.go` —— 路由挂载、`noAuth` → `public`
- 改 `main.go` —— flag 改名、banner 警告
- 改 `web/index.html` —— `icon-link` symbol、toast 容器
- 改 `web/app.js` —— 分享按钮 + toast
- 改 `web/app.css` —— 按钮和 toast 样式
- 新增 `share_test.go`

所有文件保持 500 行以内。

## 8. 进度

- [x] share.go + 测试
- [x] requireAuth 接分享 token
- [x] `-no-auth` → `-public`
- [x] 界面：分享按钮 + toast
- [x] 真机验证（签发 → curl 下载 → 越权路径被拒）

## 9. 执行记录

实现过程中改掉的两处设计：

**app.js / app.css 双双撞上 500 行上限**，按 auth.js/modal.css 已有的模式
再拆出 `web/share.js` + `web/share.css`。拆完 app.js 434 行、app.css 493 行。

**加静态文件要改三个地方**（建文件、`serveUIAsset` 加一条 case、index.html 加
标签），漏中间那条就是线上 404，而且页面不报错、只是某个 `window.trynetXxx`
静悄悄没挂上。补了 `TestUIServesEveryAssetThePageReferences`：顺着 index.html
的引用逐个抓，以后加文件不用回来改测试。

变异验证（改坏实现 → 确认测试变红 → 还原），7 条全部变红：

| 变异 | 抓住它的测试 |
| --- | --- |
| token 不再绑定路径 | TestShareTokenDoesNotUnlockOtherFiles / TestRequireAuthShareTokenScope |
| 分享 token 放行 POST | TestRequireAuthShareTokenScope |
| token 改成滑动续期 | TestShareTokenDoesNotSlideExpiry |
| 签发不检查是不是普通文件 | TestIssueShareRejectsUnshareablePaths |
| 过期 token 被接受 | TestExpiredShareTokenRejected |
| 敏感文件可签发 | TestIssueShareRejectsUnshareablePaths |
| 丢掉 /data 前缀检查 | TestIssueShareRejectsUnshareablePaths |
| 摘掉 share.js 路由 | TestUIServesEveryAssetThePageReferences |

头一轮有三条变异**仍然绿**，都是"通过的理由不对"，已重写：

- **方法限制**和 **shares==nil**：端到端路径上被"路径对不上"抢先挡掉了，
  考验不到。改成直接打 `requireAuth` 的表驱动用例。
- **不滑动续期**：Windows 上连续两次 `time.Now()` 返回**逐位相同**的值
  （实测 delta = 0s），拿 now+TTL 跟 now+TTL 比永远相等，续期了也看不见。
  改成把过期时间拨成"还有 5 分钟"这个明显不同的值。

真机验证（走 trycloudflare 隧道）：登录 → 为 report.pdf 签发 → 无凭据 curl
下载 200；同一 token 取 `/data/run.log`、`/data/.env`、`/data/docs/`、目录列表
全部 401；带 token 上传 401；签发 `.env`/`id_rsa`/目录全部 400。
`-public` 下 `/auth/share` 返回 404、`shareEnabled` 为 false、警告照常打出。
