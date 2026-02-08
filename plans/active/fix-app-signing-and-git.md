# 修复应用签名和初始化 Git

> 创建: 2026-02-08
> 状态: 进行中

## 目标

1. 解决 macOS "文件已损坏" 问题（Gatekeeper）
2. 添加 Copyright 信息到应用配置
3. 初始化 Git 仓库并推送到 GitHub

## 步骤

- [ ] 移除应用的隔离属性（临时解决方案）
- [ ] 更新 Info.plist 添加 Copyright 信息
- [ ] 更新 wails.json 添加 author 和版权信息
- [ ] 初始化 Git 仓库
- [ ] 配置 .gitignore
- [ ] 添加远程仓库
- [ ] 提交并推送代码
- [ ] 重新构建验证

## 完成标准

- [ ] 应用可以正常打开（无"已损坏"提示）
- [ ] Copyright 信息正确显示
- [ ] Git 仓库已初始化并推送到 GitHub
- [ ] 构建成功

## 备注

- GitHub Repo: git@github.com:BlakeLiAFK/trynet.git
- Copyright: 基于 GitHub repo 信息
- macOS 签名问题解决方案：xattr -cr 移除隔离属性
