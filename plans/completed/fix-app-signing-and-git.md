# 修复应用签名和初始化 Git

> 创建: 2026-02-08
> 状态: 已完成
> 完成时间: 2026-02-08 21:18

## 目标

1. 解决 macOS "文件已损坏" 问题（Gatekeeper）
2. 添加 Copyright 信息到应用配置
3. 初始化 Git 仓库并推送到 GitHub

## 步骤

- [x] 移除应用的隔离属性（临时解决方案）
- [x] 更新 Info.plist 添加 Copyright 信息
- [x] 更新 wails.json 添加 author 和版权信息
- [x] 初始化 Git 仓库
- [x] 配置 .gitignore
- [x] 添加远程仓库
- [x] 提交并推送代码
- [x] 重新构建验证

## 完成标准

- [x] 应用可以正常打开（无"已损坏"提示）
- [x] Copyright 信息正确显示
- [x] Git 仓库已初始化并推送到 GitHub
- [x] 构建成功（5.978s）

## 执行结果

### 1. macOS 签名问题解决
使用 `xattr -cr` 移除隔离属性，应用可正常打开。

### 2. Copyright 配置
更新 `wails.json`:
```json
{
  "author": {
    "name": "BlakeLiAFK",
    "email": "blake@trynet.dev"
  },
  "info": {
    "companyName": "TryNet",
    "productName": "TryNet",
    "productVersion": "1.0.0",
    "copyright": "© 2026 BlakeLiAFK. All rights reserved. https://github.com/BlakeLiAFK/trynet",
    "comments": "A modern cloudflared tunnel management tool"
  }
}
```

验证结果：
- 应用名称：TryNet
- 版本：1.0.0
- Copyright：© 2026 BlakeLiAFK. All rights reserved. https://github.com/BlakeLiAFK/trynet

### 3. Git 仓库初始化
- 初始化仓库：✅
- 远程仓库：git@github.com:BlakeLiAFK/trynet.git
- 首次提交：46e2313 (60 files, 8354 insertions)
- 推送到 main 分支：✅

### 4. .gitignore 优化
添加了构建产物、依赖、IDE、系统文件等常见忽略项。

## 备注

- GitHub Repo: git@github.com:BlakeLiAFK/trynet.git
- Copyright: 基于 GitHub repo 信息
- macOS 签名问题解决方案：xattr -cr 移除隔离属性
