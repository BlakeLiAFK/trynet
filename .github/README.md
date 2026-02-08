# GitHub Actions 说明

TryNet 项目使用 GitHub Actions 实现自动构建和发布。

## Workflows

### 1. Build and Release (`build.yml`)

**触发条件：**
- 推送 tag（如 `v1.0.1`）
- 手动触发（workflow_dispatch）

**构建平台：**
- ✅ macOS (Universal Binary - 支持 Intel + Apple Silicon)
- ✅ Windows (AMD64)
- ✅ Linux (AMD64)

**产物：**
- `TryNet-macOS-universal.tar.gz`
- `TryNet-Windows-amd64.zip`
- `TryNet-Linux-amd64.tar.gz`

**发布流程：**
1. 三个平台并行构建
2. 上传构建产物
3. 如果是 tag 推送，自动创建 GitHub Release
4. 附加所有平台的安装包

### 2. PR Check (`pr-check.yml`)

**触发条件：**
- Pull Request 到 main 分支
- 推送到 main 分支

**检查内容：**
- Go 模块验证
- 前端依赖安装
- Linux 平台构建测试

## 如何发布新版本

### 步骤 1: 更新版本号

编辑 `wails.json`：
```json
{
  "info": {
    "productVersion": "1.0.1"
  }
}
```

### 步骤 2: 提交代码

```bash
git add .
git commit -m "chore: bump version to 1.0.1"
git push
```

### 步骤 3: 创建并推送 tag

```bash
git tag v1.0.1
git push origin v1.0.1
```

### 步骤 4: 等待自动构建

- GitHub Actions 自动构建三端
- 构建时间约 10-15 分钟
- 完成后自动创建 Release

### 步骤 5: 检查 Release

访问：https://github.com/BlakeLiAFK/trynet/releases

确认三个平台的安装包都已上传。

## 手动触发构建

1. 访问 Actions 页面
2. 选择 "Build and Release" workflow
3. 点击 "Run workflow"
4. 选择分支
5. 点击 "Run workflow" 按钮

注意：手动触发不会创建 Release，只会生成 artifacts。

## 本地测试

在推送 tag 前，建议本地测试构建：

```bash
# macOS
wails build -platform darwin/universal

# Windows (需要在 Windows 上)
wails build -platform windows/amd64

# Linux (需要在 Linux 上或使用 Docker)
wails build -platform linux/amd64
```

## 依赖说明

### macOS
- 无需额外依赖（系统自带）

### Windows
- 无需额外依赖（系统自带）

### Linux
- `libgtk-3-dev`
- `libwebkit2gtk-4.0-dev`

## 故障排查

### 构建失败
1. 检查 Go 版本 (需要 1.22+)
2. 检查 Node.js 版本 (需要 20+)
3. 查看 Actions 日志

### Release 未创建
1. 确认推送的是 tag（不是分支）
2. Tag 格式必须以 `v` 开头（如 `v1.0.1`）
3. 检查 GITHUB_TOKEN 权限

### Artifacts 缺失
1. 检查构建步骤是否成功
2. 确认打包命令正确执行
3. 查看上传步骤日志
