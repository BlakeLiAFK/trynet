// thumbnail_test.go 覆盖缩略图：三种支持的格式都能出图、尺寸和长宽比对、
// 不认识的东西一律 404，以及那道"解码前就得拦住"的大小闸门。
package fileshare

import (
	"bytes"
	"encoding/json"
	"image"
	"image/color"
	"image/gif"
	"image/jpeg"
	"image/png"
	"io"
	"math/rand"
	"net/http"
	"os"
	"testing"
)

// makeImage 造一张 w×h 的测试图，内容是渐变而不是纯色——纯色图缩放前后
// 长什么样都一样，测不出降采样有没有真的在算。
func makeImage(w, h int) image.Image {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.Set(x, y, color.RGBA{uint8(x % 256), uint8(y % 256), 128, 255})
		}
	}
	return img
}

func writeImage(t *testing.T, path string, w, h int, format string) {
	t.Helper()
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	img := makeImage(w, h)
	switch format {
	case "jpeg":
		err = jpeg.Encode(f, img, nil)
	case "png":
		err = png.Encode(f, img)
	case "gif":
		err = gif.Encode(f, img, nil)
	default:
		t.Fatalf("unknown format %q", format)
	}
	if err != nil {
		t.Fatal(err)
	}
}

// ---------------------------------------------------------------------
// 降采样本身
// ---------------------------------------------------------------------

func TestDownsampleKeepsAspectRatio(t *testing.T) {
	cases := []struct {
		name         string
		w, h         int
		wantW, wantH int
	}{
		{"landscape", 800, 400, 160, 80},
		{"portrait", 400, 800, 80, 160},
		{"square", 500, 500, 160, 160},
		{"wide banner", 1600, 100, 160, 10},
		// 极端长宽比算出来另一边会是 0，必须兜到 1，否则 image.NewRGBA 得到
		// 一个空矩形，jpeg.Encode 直接报错。横竖两个方向各有一条 clamp，
		// 只测一头的话另一头那条永远跑不到
		{"extreme panorama", 4000, 3, 160, 1},
		{"extreme tower", 3, 4000, 1, 160},
	}
	for _, c := range cases {
		got := downsample(makeImage(c.w, c.h), thumbMaxEdge).Bounds()
		if got.Dx() != c.wantW || got.Dy() != c.wantH {
			t.Errorf("%s: %dx%d -> %dx%d, want %dx%d",
				c.name, c.w, c.h, got.Dx(), got.Dy(), c.wantW, c.wantH)
		}
	}
}

// 比 160 小的图原样返回，不放大——放大只会得到一张更大的糊图
func TestDownsampleDoesNotUpscale(t *testing.T) {
	for _, c := range []struct{ w, h int }{{80, 60}, {160, 160}, {10, 10}, {160, 20}} {
		got := downsample(makeImage(c.w, c.h), thumbMaxEdge).Bounds()
		if got.Dx() != c.w || got.Dy() != c.h {
			t.Errorf("%dx%d was resized to %dx%d; images at or under the cap must be left alone",
				c.w, c.h, got.Dx(), got.Dy())
		}
	}
}

// 盒式平均的意义在于"目标像素是源区域的平均值"，而不是随便挑一个。
// 造一张左半黑右半白的图，缩成 2x1 之后左边必须是黑、右边必须是白；
// 再缩成 1x1，必须是中灰——最近邻在这里会得到纯黑或纯白。
func TestDownsampleAveragesTheSourceArea(t *testing.T) {
	src := image.NewRGBA(image.Rect(0, 0, 100, 10))
	for y := 0; y < 10; y++ {
		for x := 0; x < 100; x++ {
			if x < 50 {
				src.Set(x, y, color.RGBA{0, 0, 0, 255})
			} else {
				src.Set(x, y, color.RGBA{255, 255, 255, 255})
			}
		}
	}

	half := downsample(src, 2)
	l := color.RGBAModel.Convert(half.At(0, 0)).(color.RGBA)
	r := color.RGBAModel.Convert(half.At(1, 0)).(color.RGBA)
	if l.R > 8 {
		t.Errorf("left half averaged to %d, want ~0", l.R)
	}
	if r.R < 247 {
		t.Errorf("right half averaged to %d, want ~255", r.R)
	}

	one := downsample(src, 1)
	mid := color.RGBAModel.Convert(one.At(0, 0)).(color.RGBA)
	if mid.R < 112 || mid.R > 142 {
		t.Errorf("half-black half-white averaged to %d, want mid grey (~127); "+
			"a value near 0 or 255 means it is picking one pixel, not averaging", mid.R)
	}
}

// ---------------------------------------------------------------------
// 文件 → JPEG
// ---------------------------------------------------------------------

func TestThumbnailFileHandlesEverySupportedFormat(t *testing.T) {
	dir := t.TempDir()
	for _, f := range []struct{ name, format string }{
		{"a.jpg", "jpeg"}, {"b.jpeg", "jpeg"}, {"c.png", "png"}, {"d.gif", "gif"},
	} {
		path := dir + "/" + f.name
		writeImage(t, path, 600, 300, f.format)

		data, err := thumbnailFile(path)
		if err != nil {
			t.Errorf("%s: %v", f.name, err)
			continue
		}
		// 输出必须是能解回来的合法 JPEG，不能只看"有字节返回"
		img, format, err := image.Decode(bytes.NewReader(data))
		if err != nil {
			t.Errorf("%s: thumbnail does not decode: %v", f.name, err)
			continue
		}
		if format != "jpeg" {
			t.Errorf("%s: thumbnail encoded as %q, want jpeg", f.name, format)
		}
		if b := img.Bounds(); b.Dx() != 160 || b.Dy() != 80 {
			t.Errorf("%s: thumbnail is %dx%d, want 160x80", f.name, b.Dx(), b.Dy())
		}
	}
}

func TestIsThumbnailable(t *testing.T) {
	yes := []string{"a.jpg", "a.JPG", "a.jpeg", "a.png", "a.PNG", "a.gif", "sub/dir/photo.Png"}
	// webp/heic/avif 标准库解不了，必须在打开文件之前就筛掉
	no := []string{"a.txt", "a.webp", "a.heic", "a.avif", "a.pdf", "a", "a.png.txt", "a.svg"}
	for _, n := range yes {
		if !isThumbnailable(n) {
			t.Errorf("%s should be thumbnailable", n)
		}
	}
	for _, n := range no {
		if isThumbnailable(n) {
			t.Errorf("%s should not be thumbnailable", n)
		}
	}
}

// 扩展名筛选是看声明的类型，不是看内容。一个内容是合法 PNG、但叫 .txt 的
// 文件不该出缩略图——这条用例专门挑出扩展名检查本身：如果只靠"解码失败"来
// 兜底，这个文件会解得开，缩略图就发出去了。
func TestThumbRouteGoesByExtensionNotContent(t *testing.T) {
	dir := t.TempDir()
	f, err := os.Create(dir + "/actually-a-png.txt")
	if err != nil {
		t.Fatal(err)
	}
	if err := png.Encode(f, makeImage(200, 200)); err != nil {
		f.Close()
		t.Fatal(err)
	}
	f.Close()

	addr, err := Start(Config{Dir: dir, Public: true})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	resp, err := http.Get("http://" + addr + "/thumb/actually-a-png.txt")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("a valid png named .txt: got status %d, want 404", resp.StatusCode)
	}
}

// 扩展名说是 png、内容其实不是，解码会失败，这条路必须回错误而不是 panic
func TestThumbnailFileRejectsCorruptImage(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir+"/fake.png", "this is not a png at all")
	if _, err := thumbnailFile(dir + "/fake.png"); err == nil {
		t.Error("a file that only claims to be a png produced a thumbnail")
	}
}

// ---------------------------------------------------------------------
// HTTP 层
// ---------------------------------------------------------------------

func TestThumbRouteServesAndRejects(t *testing.T) {
	dir := t.TempDir()
	writeImage(t, dir+"/photo.png", 400, 200, "png")
	writeFile(t, dir+"/notes.txt", "just text")
	writeFile(t, dir+"/fake.webp", "webp bytes")
	writeImage(t, dir+"/id_rsa.png", 100, 100, "png") // 敏感名单按整个名字匹配，这个不该命中
	writeImage(t, dir+"/server.pem.png", 100, 100, "png")

	addr, err := Start(Config{Dir: dir, Public: true})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}

	cases := []struct {
		name, path string
		want       int
	}{
		{"real image", "/thumb/photo.png", http.StatusOK},
		{"text file", "/thumb/notes.txt", http.StatusNotFound},
		{"unsupported format", "/thumb/fake.webp", http.StatusNotFound},
		{"missing file", "/thumb/nope.png", http.StatusNotFound},
		{"directory", "/thumb/", http.StatusNotFound},
		// 扩展名是 .png 所以 isThumbnailable 放行，但整个文件名不在敏感名单里
		// （名单匹配的是完整名字，不是子串），所以这两个应该正常出图
		{"name merely containing id_rsa", "/thumb/id_rsa.png", http.StatusOK},
		{"name merely containing .pem", "/thumb/server.pem.png", http.StatusOK},
	}
	for _, c := range cases {
		resp, err := http.Get("http://" + addr + c.path)
		if err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if resp.StatusCode != c.want {
			t.Errorf("%s (%s): got status %d, want %d", c.name, c.path, resp.StatusCode, c.want)
		}
		if c.want == http.StatusOK {
			if ct := resp.Header.Get("Content-Type"); ct != "image/jpeg" {
				t.Errorf("%s: Content-Type = %q, want image/jpeg", c.name, ct)
			}
			if _, _, err := image.Decode(bytes.NewReader(body)); err != nil {
				t.Errorf("%s: body is not a decodable image: %v", c.name, err)
			}
		}
	}
}

// 真正命中敏感名单的图片必须被内容过滤挡掉——/thumb 和 /data 共用同一道过滤，
// 少套一层就等于开了个绕过通道：挡住了下载，却能从缩略图里看到内容
func TestThumbRouteAppliesContentFilter(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(dir+"/.git", 0o700); err != nil {
		t.Fatal(err)
	}
	writeImage(t, dir+"/.git/secret.png", 100, 100, "png")
	writeImage(t, dir+"/.hidden.png", 100, 100, "png")
	writeImage(t, dir+"/visible.png", 100, 100, "png")

	addr, err := Start(Config{Dir: dir, Public: true})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}

	for path, want := range map[string]int{
		"/thumb/.git/secret.png": http.StatusNotFound,
		"/thumb/.hidden.png":     http.StatusNotFound,
		"/thumb/visible.png":     http.StatusOK, // 对照组，免得整批因为别的原因一起失败
	} {
		resp, err := http.Get("http://" + addr + path)
		if err != nil {
			t.Fatalf("%s: %v", path, err)
		}
		resp.Body.Close()
		if resp.StatusCode != want {
			t.Errorf("%s: got status %d, want %d", path, resp.StatusCode, want)
		}
	}
}

// 有鉴权时 /thumb 不能是个匿名入口——挡住了 /data 却放开缩略图，
// 等于把目录里每张图的内容都发出去了
func TestThumbRouteRequiresAuth(t *testing.T) {
	dir := t.TempDir()
	writeImage(t, dir+"/photo.png", 200, 200, "png")
	ts, cookie := newShareServer(t, dir)

	resp, err := http.Get(ts.URL + "/thumb/photo.png")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("without a session: got status %d, want 401", resp.StatusCode)
	}

	req, _ := http.NewRequest(http.MethodGet, ts.URL+"/thumb/photo.png", nil)
	req.AddCookie(cookie)
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("get with cookie: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("with a session: got status %d, want 200", resp.StatusCode)
	}
}

// 分享 token 对 /thumb 无效。拿到分享链接的人得到的是一个直接下载地址，
// 从来不会看到网格界面，给他开这条路只是白白多一个入口。
//
// 说清楚这条能证明什么：/thumb 那里传 nil 是"明确不收分享 token"的写法，
// 但即便传了 shares 结果也一样——token 绑的是 /data/photo.png，请求路径是
// /thumb/photo.png，路径一比就对不上。两道保险指向同一个结果，这条测试盯的
// 是结果，区分不了是哪一道在起作用。nil 留着是因为它写明了意图，不是因为
// 少了它就会破。
func TestThumbRouteIgnoresShareTokens(t *testing.T) {
	dir := t.TempDir()
	writeImage(t, dir+"/photo.png", 200, 200, "png")
	ts, cookie := newShareServer(t, dir)

	_, link := issueShare(t, ts, cookie, "/data/photo.png")
	token := tokenFromLink(t, link)

	status, _ := getNoCookie(t, ts.URL+"/thumb/photo.png?"+shareQueryParam+"="+token)
	if status != http.StatusUnauthorized {
		t.Errorf("share token on /thumb: got status %d, want 401", status)
	}
	// 对照：同一个 token 走 /data 是通的，说明 401 不是因为 token 本身坏了
	if status, _ := getNoCookie(t, ts.URL+link); status != http.StatusOK {
		t.Errorf("control: the same token on /data got %d, want 200", status)
	}
}

// 超过 thumbMaxSourceBytes 的图不做缩略图。
//
// 关键在于证明这道闸门是**解码之前**生效的，而不是"解码完了才发现太大"——
// 后者等于没拦，内存早就吃进去了。证法：造一张确实能解码的大图，断言
// /thumb 回 404，同时断言 thumbnailFile 对同一个文件是成功的。两条加起来
// 说明 404 只可能来自大小检查，不是来自解码失败。
func TestThumbRouteRefusesOversizedSourceBeforeDecoding(t *testing.T) {
	dir := t.TempDir()
	path := dir + "/huge.png"

	// 随机噪声压不动，PNG 编出来的体积接近原始像素字节数
	rng := rand.New(rand.NewSource(1))
	const side = 2100 // 2100*2100*4 ≈ 17.6 MB 原始数据，稳稳超过 12 MB 的线
	img := image.NewRGBA(image.Rect(0, 0, side, side))
	rng.Read(img.Pix)
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := png.Encode(f, img); err != nil {
		f.Close()
		t.Fatal(err)
	}
	f.Close()

	fi, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if fi.Size() <= thumbMaxSourceBytes {
		t.Fatalf("test fixture is only %d bytes, under the %d cap; it proves nothing",
			fi.Size(), thumbMaxSourceBytes)
	}

	// 这个文件本身是解得开的——所以下面的 404 只可能来自大小闸门
	if _, err := thumbnailFile(path); err != nil {
		t.Fatalf("fixture does not decode (%v); the 404 below would be ambiguous", err)
	}

	addr, err := Start(Config{Dir: dir, Public: true})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	resp, err := http.Get("http://" + addr + "/thumb/huge.png")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("oversized source: got status %d, want 404", resp.StatusCode)
	}

	// 目录列表里的 thumb 标记也得认这道闸门。不认的话前端会为这张图发一次
	// 注定 404 的请求——扩展名是 .png，光看扩展名是分辨不出来的。
	// 顺带复用上面那张造起来要两秒多的大图，不另起一份。
	list, err := http.Get("http://" + addr + "/data/")
	if err != nil {
		t.Fatalf("get listing: %v", err)
	}
	defer list.Body.Close()
	var listing dirListing
	if err := json.NewDecoder(list.Body).Decode(&listing); err != nil {
		t.Fatalf("decode listing: %v", err)
	}
	for _, e := range listing.Entries {
		if e.Name == "huge.png" && e.Thumb {
			t.Error("listing marked an oversized image as thumbnailable; " +
				"the front end would request a thumbnail that can only 404")
		}
	}
}

// 目录列表里的 thumb 标记决定前端要不要去请求缩略图。它必须只看扩展名和
// 大小——列目录时把整个目录的图都解一遍只为填一个布尔值，代价完全不成比例。
// 所以"扩展名对但内容坏"的文件这里会是 true，由 /thumb 的 404 和前端的
// onerror 兜底，这是刻意的。
func TestListingMarksWhichEntriesHaveThumbnails(t *testing.T) {
	dir := t.TempDir()
	writeImage(t, dir+"/photo.png", 100, 100, "png")
	writeImage(t, dir+"/shot.jpg", 100, 100, "jpeg")
	writeFile(t, dir+"/notes.txt", "text")
	writeFile(t, dir+"/photo.webp", "webp")
	writeFile(t, dir+"/broken.png", "not actually a png")
	if err := os.MkdirAll(dir+"/sub", 0o700); err != nil {
		t.Fatal(err)
	}

	addr, err := Start(Config{Dir: dir, Public: true})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	resp, err := http.Get("http://" + addr + "/data/")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	defer resp.Body.Close()

	var listing dirListing
	if err := json.NewDecoder(resp.Body).Decode(&listing); err != nil {
		t.Fatalf("decode listing: %v", err)
	}

	want := map[string]bool{
		"photo.png":  true,
		"shot.jpg":   true,
		"notes.txt":  false,
		"photo.webp": false,
		"broken.png": true, // 扩展名对就标 true，不为此解码
		"sub":        false,
	}
	seen := map[string]bool{}
	for _, e := range listing.Entries {
		seen[e.Name] = true
		if w, ok := want[e.Name]; ok && e.Thumb != w {
			t.Errorf("%s: thumb = %v, want %v", e.Name, e.Thumb, w)
		}
	}
	for name := range want {
		if !seen[name] {
			t.Errorf("%s missing from the listing", name)
		}
	}
}
