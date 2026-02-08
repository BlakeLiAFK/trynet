# 更新系统托盘图标

> 创建: 2026-02-08
> 状态: 已完成
> 完成时间: 2026-02-08 21:26

## 目标

更新系统托盘（菜单栏）的小图标，使其与应用主图标保持一致。

## 当前状态

- 旧 systray icon: `internal/tray/icon.png` (22x22, 831B)
- 应用主图标: `build/appicon.png` (512x512, 95KB)

## 步骤

- [x] 从 appicon.png 生成适合 systray 的小图标
- [x] 替换 internal/tray/icon.png
- [x] 重新构建应用
- [x] 测试托盘图标显示效果

## 完成标准

- [x] Systray icon 显示清晰
- [x] 与应用主题一致
- [x] macOS 菜单栏显示正常

## 执行结果

使用 sips 命令从主图标生成了两个版本：
- `icon.png`: 22x22 (667B) - 标准分辨率
- `icon@2x.png`: 44x44 (1.7KB) - Retina 分辨率

构建时间：4.561s
应用已启动，托盘图标已更新

## 备注

macOS systray icon 建议尺寸：
- 标准: 22x22
- Retina: 44x44 (推荐使用 @2x 命名)
