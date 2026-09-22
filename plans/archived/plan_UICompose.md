# plan_UICompose —— 文件服务页面视觉构图重排

只动 `web/index.html`、`web/app.css`、`web/app.js`，外加 favicon 路由（`fileserver.go` +
一个新测试）。后端逻辑、路由、安全校验不碰。

## 现状问题

1. 拖放区常驻 720×200，是页面最重的元素，但它是次要动作；文件列表才是内容，
   现在视觉上最弱。
2. 拖放区是个大白盒，200px 高只放三个居中小元素，像未完成的占位符。
3. 列表没有承载面：行是裸文本浮在页面背景上，没有卡片、没有分隔线。
4. 下载箭头只在 hover 出现（`@media (hover:hover)`），静止态看不出行能点，
   触摸设备等于没有箭头。
5. 页头没存在感，`trynet /` 小字浮在左上角。

## 改法

### 1. 拖放区：整页拖放 + 顶部按钮

- 目录非空：不再显示常驻大盒子。上传入口挪到页头右侧一个按钮
  （Upload 文字 + 图标，radius 8px，accent 描边，高 36px），点它触发隐藏的
  file input。
- 拖文件到窗口任意位置：`document` 级监听 `dragenter/dragover/dragleave/drop`，
  弹出整页覆盖层（`position:fixed;inset:0`，半透明 `--bg` + 居中虚线圆角框 +
  "Release to upload"），120ms 淡入。
  `dragleave` 用一个进入计数器（`dragenter` +1，`dragleave` -1，计数归零才真正
  判定"离开"）来防止子元素间移动时的误触发，比判断 `relatedTarget` 更稳（覆盖层
  本身还会在拖拽过程中插入/移除 DOM，`relatedTarget` 容易失真）。
- 目录为空：保留大拖放区，文案 `No files yet. Drop one in.`。

### 2. 列表承载面

容器：`background:var(--surface); border:1px solid var(--border);
border-radius:12px; overflow:hidden`。
行高 52px，行间 1px `--border` 分隔线（最后一行无），无外边距。
桌面端行内三段式：`[图标20px][文件名 flex:1][大小 右对齐等宽13px muted][日期13px muted][下载箭头]`。
窄屏 <640px：大小/日期折到文件名下面一行，下载箭头常驻。

### 3. 下载箭头静止态可见

`opacity:.35`，hover/focus 时 `opacity:1`，去掉 `@media (hover:hover)` 隐藏写法。

### 4. 页头

高 56px，底部 1px 分隔线。左：`trynet`（600 字重 15px accent 色）+ 面包屑
（muted，可点击回上级）。右：Upload 按钮。不用 sticky。

### 5. 纵向节奏

顶栏到列表 20px，左右留白桌面 24px / 手机 16px，最大宽度 820px 居中。

### 6. favicon

`assets/trynet-icon.png` 复制一份到 `web/icon.png`（embed 范围要求目录必须是
`web/`），`fileserver.go` 里 `serveUIAsset` 加一支 `/ui/icon.png` 分支，
`index.html` 头部加 `<link rel="icon" href="/ui/icon.png">`。
补一个 `TestUIServesIcon` 测试，仿照 `TestUIServesAppCSS` 的写法。

## 不碰的东西

后端路由结构、鉴权、内容过滤、路径校验、上传进度（3px 细线）、深浅色切换、
`prefers-reduced-motion`、键盘可达性、点击目标 ≥44px 全部保留。

## 验证

```
GOTOOLCHAIN=go1.26.8 gofmt -l .
GOTOOLCHAIN=go1.26.8 go vet ./...
GOTOOLCHAIN=go1.26.8 go test -count=1 ./...
GOTOOLCHAIN=go1.26.8 go test -count=2 ./...
wc -l *.go web/*
```

## 状态

- [x] 计划写出
- [x] index.html：页头结构、Upload 按钮、拖放覆盖层元素、favicon link
- [x] app.css：页头/按钮/覆盖层/列表承载面/箭头静止态/纵向节奏
- [x] app.js：整页拖放事件（计数器防误触发）、按钮触发 file input、覆盖层显隐
- [x] fileserver.go + web/icon.png：favicon 路由
- [x] fileserver_route_test.go：TestUIServesIcon
- [x] 跑四条验证命令（gofmt/vet/test x2 全绿，126 个测试函数：125 通过 + 1 跳过，跳过项和本次改动无关，是既有用例）
- [x] 按主题分两个 commit：布局重排一个、favicon 一个
- [x] 归档计划、更新 TaskTable.md
- [x] 写 `.superpowers/sdd/ui-compose-report.md`

## 额外发现并顺手修的一处坑

用真实浏览器（chrome-devtools MCP）截图走查时发现：面包屑只列出当前路径
往上的那几段，根目录本身不出现在里面；如果导航到一个"空的一级子目录"
（entries 为空，列表面板整个不渲染），面包屑上没有任何可点的段能回根目录，
用户会被卡在那个空目录里出不来（除非用浏览器后退）。
补法：把页头的 "trynet" 品牌字做成一个链接，点击回根目录，`app.js` 里
`brandLink` 的 `click` 直接 `navigate('/')`。这一处也在两次截图验证里
确认过（root → sub → empty 全程可达可退）。
