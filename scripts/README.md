# Scripts 工具集

项目辅助脚本集合

## round_icon.py

圆角图标生成工具，用于将方形图标处理成圆角图标。

### 功能

- 为图标添加圆角效果
- 支持自定义圆角半径
- 保持透明背景
- 适用于 macOS 系统托盘图标

### 依赖

```bash
pip3 install Pillow
```

### 用法

```bash
# 基本用法（覆盖原文件，默认 20% 圆角）
python3 scripts/round_icon.py icon.png

# 输出到新文件
python3 scripts/round_icon.py icon.png icon_rounded.png

# 自定义圆角半径（25%）
python3 scripts/round_icon.py icon.png icon_rounded.png 0.25
```

### 参数说明

- `input_icon`: 输入图标路径
- `output_icon`: 输出图标路径（可选，默认覆盖原文件）
- `radius_ratio`: 圆角半径占图片宽度的比例，范围 0.0-0.5（可选，默认 0.2）

### 示例

```bash
# 处理系统托盘图标
python3 scripts/round_icon.py internal/tray/icon.png
python3 scripts/round_icon.py internal/tray/icon@2x.png
```

### 技术细节

- 使用 PIL (Pillow) 图像处理库
- 创建圆角矩形蒙版
- 保持 RGBA 透明通道
- 输出格式：PNG
