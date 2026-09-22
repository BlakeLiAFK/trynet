// thumbnail.go 给网格模式生成缩略图：解码原图、降采样到 160px、输出 JPEG。
//
// 只认标准库自带解码器的三种格式（jpeg / png / gif）。HEIC、WebP、AVIF 一律
// 回 404，前端看到 404 就显示文件图标——为了几个格式引第三方解码库，代价比
// 收益大得多。
//
// 缩略图不缓存。这是个临时分享工具，同一张图被反复请求的概率低，内存缓存的
// 复杂度（容量上限、淘汰、并发）换不回什么；浏览器那边有 Cache-Control 自己缓存。
package fileshare

import (
	"bytes"
	"image"
	"image/gif"
	"image/jpeg"
	"image/png"
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

const (
	thumbMaxEdge = 160 // 缩略图最长边（CSS 像素的两倍够用，网格卡片实际显示 ~80px）
	thumbQuality = 80

	// thumbMaxSourceBytes 是肯降解码的源文件大小上限。
	//
	// 真正要防的是内存：解码占用按**像素数**算，跟文件大小不成正比——一张
	// 100 MP 的 PNG 压缩后可能只有几 MB，解码成 RGBA 就是 400 MB。文件大小
	// 是个粗糙但够用的闸门，关键是它必须在 Decode 之前生效，不能等解码完了
	// 才发现太大。这个进程同时还在转发隧道流量，不能让一张图把它撑爆。
	thumbMaxSourceBytes = 12 << 20
)

// thumbExts 是会尝试生成缩略图的扩展名，对应标准库的三个解码器。
// 扩展名只用来快速筛掉明显不是图片的文件，真正的判断是解码成不成功。
var thumbExts = map[string]struct{}{
	".jpg": {}, ".jpeg": {}, ".png": {}, ".gif": {},
}

// isThumbnailable 按扩展名判断值不值得打开这个文件，不分大小写。
func isThumbnailable(name string) bool {
	_, ok := thumbExts[strings.ToLower(filepath.Ext(name))]
	return ok
}

// serveThumbnail 是 /thumb/ 的 handler。任何一步失败都回 404 而不是 500：
// 对调用方来说"这个文件没有缩略图"和"这个文件不是图片"是同一件事，前端的
// 处理也一样（显示文件图标），分出两种状态码只会让前端多写一个分支。
func (fs *fileService) serveThumbnail(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	local, err := resolveRequestPath(fs.dir, r.URL.Path)
	if err != nil || !isThumbnailable(local) {
		http.NotFound(w, r)
		return
	}

	fi, err := os.Stat(local)
	if err != nil || !fi.Mode().IsRegular() || fi.Size() > thumbMaxSourceBytes {
		http.NotFound(w, r)
		return
	}

	data, err := thumbnailFile(local)
	if err != nil {
		http.NotFound(w, r)
		return
	}

	w.Header().Set("Content-Type", "image/jpeg")
	// 缩略图是从磁盘上的文件现算的，内容跟着源文件走。private 是因为链接可能
	// 带着分享 token，不该被中间代理存下来给别人。
	w.Header().Set("Cache-Control", "private, max-age=300")
	w.Write(data)
}

// thumbnailFile 打开、解码、降采样、编码，返回 JPEG 字节。
func thumbnailFile(path string) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	// image.Decode 按注册的格式嗅探，上面 import 的三个包各自在 init 里注册。
	// 扩展名对不上真实格式时（.png 其实是 webp）这里会失败，正好回落到 404。
	src, _, err := image.Decode(f)
	if err != nil {
		return nil, err
	}

	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, downsample(src, thumbMaxEdge), &jpeg.Options{Quality: thumbQuality}); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// downsample 把图缩到最长边不超过 maxEdge，保持长宽比。已经够小的图原样返回，
// 不放大——放大只会得到一张更大的糊图。
//
// 用盒式平均而不是 image/draw 的最近邻：缩小时最近邻是直接丢像素，细节多的
// 照片会糊成一片摩尔纹。盒式平均就是把目标像素对应的那块源区域求个平均值，
// 多写二十行，换来的画质差别在 160px 上一眼能看出来。
func downsample(src image.Image, maxEdge int) image.Image {
	b := src.Bounds()
	sw, sh := b.Dx(), b.Dy()
	if sw <= 0 || sh <= 0 {
		return src
	}
	if sw <= maxEdge && sh <= maxEdge {
		return src
	}

	dw, dh := sw, sh
	if sw >= sh {
		dw = maxEdge
		dh = sh * maxEdge / sw
	} else {
		dh = maxEdge
		dw = sw * maxEdge / sh
	}
	// 极端长宽比（比如 4000x3 的全景条）算出来另一边可能是 0，至少留 1 像素
	if dw < 1 {
		dw = 1
	}
	if dh < 1 {
		dh = 1
	}

	dst := image.NewRGBA(image.Rect(0, 0, dw, dh))
	for y := 0; y < dh; y++ {
		// 目标第 y 行对应源图的 [y0, y1) 这一段
		y0 := b.Min.Y + y*sh/dh
		y1 := b.Min.Y + (y+1)*sh/dh
		if y1 <= y0 {
			y1 = y0 + 1
		}
		for x := 0; x < dw; x++ {
			x0 := b.Min.X + x*sw/dw
			x1 := b.Min.X + (x+1)*sw/dw
			if x1 <= x0 {
				x1 = x0 + 1
			}

			var rs, gs, bs, as uint64
			for sy := y0; sy < y1; sy++ {
				for sx := x0; sx < x1; sx++ {
					// RGBA() 返回 16 位、已经预乘 alpha 的分量
					cr, cg, cb, ca := src.At(sx, sy).RGBA()
					rs += uint64(cr)
					gs += uint64(cg)
					bs += uint64(cb)
					as += uint64(ca)
				}
			}
			n := uint64((y1 - y0) * (x1 - x0))
			i := dst.PixOffset(x, y)
			dst.Pix[i+0] = uint8(rs / n >> 8)
			dst.Pix[i+1] = uint8(gs / n >> 8)
			dst.Pix[i+2] = uint8(bs / n >> 8)
			dst.Pix[i+3] = uint8(as / n >> 8)
		}
	}
	return dst
}

// 让 gif 和 png 的解码器注册进 image.Decode。它们只在 init 里起作用，
// 代码里不直接引用，写成这样是为了不被 goimports 当成未使用的导入删掉。
var (
	_ = gif.Decode
	_ = png.Decode
)
