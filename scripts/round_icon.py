#!/usr/bin/env python3
"""
圆角图标生成脚本

将方形图标处理成圆角图标，适用于 macOS 系统托盘
支持标准和 Retina 分辨率
"""

import sys
from PIL import Image, ImageDraw
from pathlib import Path


def add_rounded_corners(image: Image.Image, radius: int) -> Image.Image:
    """
    为图片添加圆角

    Args:
        image: PIL Image 对象
        radius: 圆角半径（像素）

    Returns:
        添加圆角后的 Image 对象
    """
    # 创建圆角蒙版
    mask = Image.new('L', image.size, 0)
    draw = ImageDraw.Draw(mask)

    # 绘制圆角矩形
    draw.rounded_rectangle(
        [(0, 0), image.size],
        radius=radius,
        fill=255
    )

    # 转换为 RGBA（如果不是）
    if image.mode != 'RGBA':
        image = image.convert('RGBA')

    # 应用蒙版
    output = Image.new('RGBA', image.size, (0, 0, 0, 0))
    output.paste(image, (0, 0))
    output.putalpha(mask)

    return output


def process_icon(input_path: str, output_path: str = None, radius_ratio: float = 0.2):
    """
    处理图标文件，添加圆角

    Args:
        input_path: 输入图标路径
        output_path: 输出图标路径（可选，默认覆盖原文件）
        radius_ratio: 圆角半径占图片宽度的比例（默认 0.2 即 20%）
    """
    input_file = Path(input_path)

    if not input_file.exists():
        print(f"❌ 错误：文件不存在 {input_path}")
        sys.exit(1)

    # 读取图片
    try:
        image = Image.open(input_file)
        print(f"📖 读取图片：{input_file.name} ({image.size[0]}x{image.size[1]})")
    except Exception as e:
        print(f"❌ 读取图片失败：{e}")
        sys.exit(1)

    # 计算圆角半径
    width = image.size[0]
    radius = int(width * radius_ratio)
    print(f"🔄 处理圆角：半径 {radius}px ({radius_ratio*100}%)")

    # 添加圆角
    rounded_image = add_rounded_corners(image, radius)

    # 确定输出路径
    if output_path is None:
        output_path = input_file
    else:
        output_path = Path(output_path)

    # 保存图片
    try:
        rounded_image.save(output_path, 'PNG')
        print(f"✅ 保存成功：{output_path}")
    except Exception as e:
        print(f"❌ 保存失败：{e}")
        sys.exit(1)


def main():
    """主函数"""
    if len(sys.argv) < 2:
        print("用法：")
        print("  python3 round_icon.py <input_icon> [output_icon] [radius_ratio]")
        print("")
        print("参数：")
        print("  input_icon    输入图标路径")
        print("  output_icon   输出图标路径（可选，默认覆盖原文件）")
        print("  radius_ratio  圆角半径比例 0.0-0.5（可选，默认 0.2）")
        print("")
        print("示例：")
        print("  python3 round_icon.py icon.png")
        print("  python3 round_icon.py icon.png icon_rounded.png")
        print("  python3 round_icon.py icon.png icon_rounded.png 0.25")
        sys.exit(1)

    input_icon = sys.argv[1]
    output_icon = sys.argv[2] if len(sys.argv) >= 3 else None
    radius_ratio = float(sys.argv[3]) if len(sys.argv) >= 4 else 0.2

    # 验证参数
    if not 0 <= radius_ratio <= 0.5:
        print("❌ 错误：radius_ratio 必须在 0.0 到 0.5 之间")
        sys.exit(1)

    # 处理图标
    process_icon(input_icon, output_icon, radius_ratio)


if __name__ == '__main__':
    main()
