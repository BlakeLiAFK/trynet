// trynet 是 cloudflared 快速隧道（trycloudflare.com）的迷你实现。
// 只保留两件事：把一个本地 HTTP 端口映射到公网，或者把一个本地目录当文件服务映射出去。
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"path/filepath"

	"time"
	"trynet/internal/buildinfo"
	"trynet/internal/cfapi"
	"trynet/internal/fileshare"
	"trynet/internal/tunnel"
)

func main() {
	log.SetFlags(log.Ltime)

	port := flag.Int("port", envOrInt("TRYNET_PORT", 0), "local port to expose")
	dir := flag.String("dir", envOr("TRYNET_DIR", ""), "local directory to share (served on a random port)")
	domain := flag.String("domain", envOr("TRYNET_DOMAIN", ""), "fixed domain to publish on, via a Cloudflare named tunnel")
	apiToken := flag.String("api-token", envOr("CLOUDFLARE_API_TOKEN", ""), "Cloudflare API token (required with -domain)")
	accountID := flag.String("account-id", envOr("CLOUDFLARE_ACCOUNT_ID", ""), "Cloudflare account id (only needed if the token can access more than one account)")
	teardown := flag.Bool("teardown", envOrBool("TRYNET_TEARDOWN", false), "delete the auto-created tunnel and dns record on exit (only with -domain)")
	forceNew := flag.Bool("new", envOrBool("TRYNET_NEW", false), "ignore saved quick tunnel credentials and request a new tunnel")
	bypass := flag.Bool("bypass", envOrBool("TRYNET_BYPASS", false), "disable all content filtering (dotfiles and sensitive file names) on the file server -- an escape hatch, use with care")
	readOnly := flag.Bool("read-only", envOrBool("TRYNET_READ_ONLY", false), "serve the directory without accepting uploads (-dir mode only)")
	upload := flag.Bool("upload", envOrBool("TRYNET_UPLOAD", false), "accept uploads even in -public mode; without -public uploads are on unless -read-only")
	public := flag.Bool("public", envOrBool("TRYNET_PUBLIC", false), "serve the files with no login at all -- anyone with the url gets in (-dir mode only)")
	user := flag.String("user", envOr("TRYNET_USER", "admin"), "login username for the file server (-dir mode only)")
	pass := flag.String("pass", envOr("TRYNET_PASS", ""), "login password for the file server, random if empty (-dir mode only)")
	showVersion := flag.Bool("version", false, "print version and source commit, then exit")
	flag.Parse()
	if *showVersion {
		fmt.Printf("trynet v%s (%s)\n", buildinfo.Version, buildinfo.Commit)
		return
	}

	if *port != 0 && *dir != "" {
		log.Fatal("use either -port or -dir, not both")
	}
	// 什么都不给就分享当前目录，这是最常用的情况
	if *port == 0 && *dir == "" {
		*dir = "."
	}
	// 这几个参数只在固定域名模式下有意义，没给 -domain 时会被静默忽略，
	// 提示一下避免用户以为在跑固定域名模式、实际却跑了快速隧道
	if *domain == "" && (*teardown || *apiToken != "" || *accountID != "") {
		log.Print("warning: -teardown/-api-token/-account-id have no effect without -domain")
	}

	banner()

	// target 是给 ready 框下面那行用的描述，tunnel.Dial 选定路径之后跟路由标签
	// 拼在一起打印；这里先按模式把它定下来，成不成功还不知道，不急着打印
	origin := fmt.Sprintf("127.0.0.1:%d", *port)
	var target string
	// extraLine 是 ready 框下面、target/route 那行之后再打的一行（鉴权信息、
	// upload 开没开），只有 -dir 模式才有内容，-port 模式始终留空。
	var extraLine string
	if *dir != "" {
		abs, err := filepath.Abs(*dir)
		if err != nil {
			abs = *dir
		}

		resolvedPass := *pass
		if !*public && resolvedPass == "" {
			resolvedPass, err = fileshare.GeneratePassword(fileshare.PasswordDigits)
			if err != nil {
				log.Fatalf("generate password: %v", err)
			}
		}

		cfg := fileshare.Config{
			Dir:    *dir,
			Bypass: *bypass,
			Upload: uploadAllowed(*public, *upload, *readOnly),
			Public: *public,
			User:   *user,
			Pass:   resolvedPass,
		}
		// -bypass 关掉点文件和敏感名单两道过滤，读写两侧都放开，是纯粹的逃生舱——
		// 打开之后分享目录里任何东西都能被列出、下载、上传覆盖，必须打得足够显眼。
		if cfg.Bypass {
			log.Print("WARNING: -bypass is enabled -- dotfile and sensitive file name filtering is OFF for this file server")
		}
		// 公开 + 可写是最开放的组合，值得单独一行警告：链接一旦流出去，
		// 任何人都能往这个目录里塞东西
		if *public && *upload && *readOnly {
			log.Print("warning: -upload and -read-only are both set; -read-only wins and uploads stay off")
		}
		if cfg.Public && cfg.Upload {
			log.Print("WARNING: -public and -upload are both on -- anyone with the url can write into this directory")
		}
		if origin, err = fileshare.Start(cfg); err != nil {
			log.Fatalf("file server: %v", err)
		}
		target = "serving " + abs

		// 一律读 cfg.Upload（uploadAllowed 算完的结果），不是 *upload 那个
		// 原始 flag——-read-only 和 -public 都会改变最终值，照着 flag 打会撒谎
		switch {
		case !cfg.Public:
			extraLine = fileServiceStatusLine(*user, resolvedPass, cfg.Upload)
		case cfg.Upload:
			extraLine = uploadOnlyStatusLine()
		}
	} else {
		target = "forwarding to " + origin
	}

	var info *tunnel.Info
	var refresh func(context.Context) (*tunnel.Info, error)
	var teardownFn func() error
	// announceStore 只在这次启动是"新建"存档（而不是复用已有存档）时为 true，
	// 用来控制要不要在 ready 框下面多打一行"存到哪了、记得加 .gitignore"的提示——
	// 复用存档时用户已经知道这回事，不用每次重复说。
	var announceStore bool

	// 信号处理要在这里就建好，而不是等凭据都准备完——不然下面的
	// tunnel.RequestQuick/cfapi.SetupNamedTunnel 卡住时，Ctrl+C 没人接，只能强杀进程。
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)

	if *domain != "" {
		printTokenInfo(*apiToken, tokenSource())
		if *apiToken == "" {
			log.Fatal("no Cloudflare API token: set CLOUDFLARE_API_TOKEN or pass -api-token")
		}

		setupCtx, cancel := context.WithTimeout(ctx, 60*time.Second)
		setupInfo, handle, err := cfapi.SetupNamedTunnel(setupCtx, *apiToken, *accountID, *domain)
		cancel()
		if err != nil {
			log.Fatalf("named tunnel setup: %v", err)
		}
		info = setupInfo
		// handle 会在 refresh 里被重新赋值（隧道被外部删掉、需要重建的时候），
		// 所以 refresh/teardownFn 都要在调用时才读 handle，不能提前绑定成方法值，
		// 否则重建之后 teardownFn 还会拿旧 handle 去删一个已经不存在的隧道。
		refresh = func(ctx context.Context) (*tunnel.Info, error) {
			refreshCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
			defer cancel()
			fresh, err := handle.RefreshToken(refreshCtx)
			if err == nil {
				return fresh, nil
			}
			if !cfapi.IsNotFound(err) {
				return nil, err
			}
			// 固定域名模式下"注册被拒绝"最常见的原因就是隧道被删了（dashboard 手删，
			// 或者上一次 teardown 误删——现在已经修掉，但存量场景仍可能遇到）。
			// 单纯重取 token 只会一直 404，永远好不了，必须把整条链路重新建一遍。
			log.Print("named tunnel no longer exists on cloudflare's side, recreating it")
			setupCtx, cancel := context.WithTimeout(ctx, 60*time.Second)
			defer cancel()
			newInfo, newHandle, setupErr := cfapi.SetupNamedTunnel(setupCtx, *apiToken, *accountID, *domain)
			if setupErr != nil {
				return nil, fmt.Errorf("recreate named tunnel: %w", setupErr)
			}
			handle = newHandle
			return newInfo, nil
		}
		if *teardown {
			teardownFn = func() error { return handle.Teardown() }
		}
	} else {
		// refresh 包一层 tunnel.RequestQuick：不管是这里首次申请，还是
		// run() 里注册被拒之后重新申请，拿到的新凭据都要覆盖写回存档，
		// 否则下次重启还会先去试那份已经失效的。
		refresh = func(ctx context.Context) (*tunnel.Info, error) {
			fresh, err := tunnel.RequestQuick(ctx)
			if err != nil {
				return nil, err
			}
			if err := tunnel.SaveStore(fresh); err != nil {
				log.Printf("save tunnel store: %v", err)
			}
			return fresh, nil
		}

		fetched, isNew, err := tunnel.AcquireQuick(ctx, *forceNew, refresh)
		if err != nil {
			log.Fatalf("quick tunnel request: %v", err)
		}
		info = fetched
		announceStore = isNew
	}

	run(ctx, info, origin, target, extraLine, refresh, announceStore)
	// run 一返回就把信号处理还给系统默认行为：teardown 最长要 90 秒，期间
	// 用户再按 Ctrl+C 得能正常杀掉进程，而不是被 NotifyContext 已经退出、
	// 却没人再读的信号 channel 吞掉。
	stop()

	if teardownFn != nil {
		log.Print("tearing down the auto-created tunnel and dns record")
		if err := teardownFn(); err != nil {
			log.Printf("teardown: %v", err)
		} else {
			log.Print("teardown: done")
		}
	}
}

// run 维持一条到边缘的连接，断了就换一个 region 重连。
// 只有注册被边缘明确拒绝（凭据大概率已失效）才重新申请隧道，
// 单纯网络抖动不换隧道，否则公网地址会跟着变，用户还得重新复制链接。
func run(ctx context.Context, info *tunnel.Info, origin, target, extraLine string, refresh func(context.Context) (*tunnel.Info, error), announceStore bool) {
	bo := tunnel.NewBackoff()
	// reconnect 一旦成功连过一次就变 true，往后的每次重连都算 reconnect：
	// 首次连接安静地把信息折进 ready 框下面那行，重连则要单独打一行让用户
	// 知道"断了但又活了"，两者不能共用同一套打印逻辑。
	reconnect := false

	// 隧道包只在该说话的时候通知一声，具体打成什么样在这里决定。
	// hostname 是回调传出来的那一份，不读闭包外的 info——info 会在注册被拒时
	// 被换成新的一份，闭包捕获到的随时可能是上一条隧道的。
	cb := tunnel.Callbacks{
		OnReady: func(location, hostname, route string) {
			printReady(location, hostname)
			printServing(target, route)
			if extraLine != "" {
				fmt.Println(extraLine)
			}
			if announceStore {
				printStoreHint()
			}
		},
		OnReconnect: func(route string) {
			log.Printf("reconnected via %s", route)
		},
	}

	for i := 0; ctx.Err() == nil; i++ {
		host := tunnel.EdgeHosts[i%len(tunnel.EdgeHosts)]
		result := tunnel.Serve(ctx, info, origin, host, reconnect, cb)
		if ctx.Err() != nil {
			return
		}

		if result.Rejected {
			log.Printf("tunnel registration rejected: %v; requesting a new tunnel", result.Err)
			if fresh, err := refresh(ctx); err != nil {
				log.Printf("refresh tunnel: %v", err)
			} else {
				info = fresh
			}
		}
		if result.Connected {
			bo.Reset()
			reconnect = true
		}

		wait := bo.Next()
		log.Printf("connection to %s ended: %v; retrying in %s", host, result.Err, wait)
		if result.DialFailed {
			// 先让用户看到发生了什么，再给建议；只有拨号本身失败才算，
			// 注册被拒、连接正常断开都不是"边缘连不上"，不该触发这条提示
			printProxyHint()
		}
		if !tunnel.SleepCtx(ctx, wait) {
			return
		}
	}
}

// uploadAllowed 决定这次运行收不收上传。三个参数的关系是刻意不对称的：
//
//   - 默认（有鉴权）：收。能写的人是拿到那串随机密码的人，风险可控；
//     要求用户记得敲 -upload 才能用上传，等于把功能藏起来
//   - -public：不收，除非显式 -upload。公网上一个谁都能塞东西的目录，
//     不该是敲一行 -public 的副作用
//   - -read-only：一律不收，压过上面两条。它是个明确的"关掉"意图，
//     跟 -upload 撞车时以它为准（调用方会为此打一行提示）
func uploadAllowed(public, upload, readOnly bool) bool {
	if readOnly {
		return false
	}
	if public {
		return upload
	}
	return true
}
