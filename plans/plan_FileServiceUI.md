# plan_FileServiceUI —— 路由重构 + 敏感过滤 + 拖拽上传界面

承接 `plan_FileService.md`（鉴权 + 上传的第一版）。这一份是在它之上的三件事。

## 1. 路由：`/ui` 与 `/data` 彻底分开

第一版把页面和文件混在同一个 URL 空间里，需要靠"不太可能重名的前缀"来避让。
改成两个挂载点之后，碰撞在结构上就不可能发生。

```
GET  /                  → 302 /ui/
GET  /ui/               → index.html        ┐
GET  /ui/app.css        → embed.FS          ├ 页面：不过滤
GET  /ui/app.js         → embed.FS          ┘

GET  /data/             → 目录列表 JSON     ┐
GET  /data/sub/         → 子目录列表        ├ 数据：过滤 + 路径校验
GET  /data/report.pdf   → 文件字节          │
POST /data/sub/         → 上传到该目录      ┘
```

- 鉴权覆盖全部路径
- 内容过滤只作用于 `/data/*`
  （这一点很关键：第一版若把过滤套在页面上，自己的 CSS 会先 404）
- 已知取舍：直接文件链接多一层前缀
  （`/hello.txt` → `/data/hello.txt`），换命名空间干净，接受

## 2. 资源改用 embed.FS

```
web/
  index.html
  app.css
  app.js
```

```go
//go:embed web
var webFS embed.FS
```

理由：真实文件有语法高亮和格式化；更重要的是避开一个具体的坑——
JS 的模板字符串和 Go 的原始字符串都用反引号，写在一个文件里必须拆成字符串拼接，
很快就没法维护。

注意 Go 的规则：`//go:embed web` 会自动跳过 `_` 或 `.` 开头的文件，
所以目录名必须是 `web/`。URL 前缀是路由层的事，与此无关。

## 3. 敏感内容过滤 + `-bypass`

点文件规则漏掉一大类：**很多敏感文件不以点开头**。
`id_rsa`、`*.pem`、`*.key`、`credentials` 现在会被原样发布。

### 名单（不分大小写）

目录/文件名：
```
.git  .svn  .hg  .ssh  .aws  .gnupg  .kube  .docker
.env  .netrc  .npmrc  .pypirc  .htpasswd  .trynet.json
id_rsa  id_dsa  id_ecdsa  id_ed25519  credentials  secring.gpg
```

扩展名：
```
.pem  .key  .pfx  .p12  .jks  .keystore  .kdbx  .ppk
```

不以点开头的那批是真正的漏网之鱼，点文件规则完全覆盖不到。

**刻意不做成大而全的清单**。清单越长越像回事，但只能挡住想得到的东西。
真正的防线是"别把含敏感文件的目录分享出去"，清单只是最后一道网；
列太长反而给人虚假的安全感。

### `-bypass`

- 语义：**关掉全部内容过滤**（点文件 + 敏感名单），是逃生舱
- 取代第一版的 `-hidden`（两个开关管同一件事容易搞混，`-hidden` 刚加还没人用）
- 环境变量 `TRYNET_BYPASS`
- 开启时打一行醒目警告说明过滤已关闭
- 过滤同时作用于**读**（列表/下载）和**写**（上传的文件名）

## 4. 界面

### 设计依据

这个工具已经有视觉身份：ASCII logo、`#F6821F` 橙、`✓ tunnel ready` 的方框、
等宽日志。网页是同一个程序的另一半，不是套通用文件浏览器皮。

### Token

```
--flame  #F6821F   已有的橙，只用在一处
--ember  #C2410C   hover / active
--ink    #1A1714   暖黑，不是纯黑
--paper  #FAF7F2   浅色底
--ash    #8A8178   次要信息（大小、时间）
```

跟随 `prefers-color-scheme` 自动切换深浅。

字体全等宽（`ui-monospace` 系统栈）：文件名和大小要对齐才好读，
而且这是终端的母语。圆角一律 0。

### 签名元素：上传渲染成终端输出

不是"带云朵图标的卡片"，是每个上传中的文件打印成一行：

```
┌─────────────────────────────────────────────┐
│                                             │
│      Drop files here or tap to choose       │
│                                             │
└─────────────────────────────────────────────┘
  · report.pdf        2.4 MB   ████████░░  78%
  · photo.jpg         1.1 MB   ██████████  done
```

拖拽悬停时边框由虚线切为实线橙色。

### 手机

- **拖放区同时是点击选择按钮**（`<label>` 包隐藏 input）——
  手机没法拖，这条漏了整个功能在手机上就废了
- 窄屏时文件名单独一行，大小与时间挪到第二行
- 点击目标 ≥44px
- 不允许任何只靠 hover 才出现的功能

### 文案（英文，主动语态，不卖弄）

```
空闲     Drop files here or tap to choose
拖入     Release to upload
空目录   No files yet. Drop one in.
失败     Upload failed — <具体原因>
```

### 刻意砍掉

页头的 ASCII logo。手机上 6 行 logo 吃掉整个首屏，
而这个页面的职责是搬文件。换成一行紧凑的主机名。

## 5. 测试要求

- 路由：`/` 跳转、`/ui/*` 不被过滤、`/data/*` 被过滤
- 敏感名单：读和写两侧都要测；`id_rsa`、`x.pem`、`.git/config` 均 404；
  普通文件 200
- `-bypass`：开启后上述全部放行
- 大小写不敏感：`ID_RSA`、`X.PEM` 同样被挡
- 现有测试全部照常通过

## 6. 进度

- [x] 路由重构 + embed.FS
- [x] 敏感过滤 + `-bypass`
- [x] 界面（视觉方向按沟通改为现代风格，终端字符画进度条作废）
- [ ] 真机验证（浏览器拖拽 + 手机访问）
