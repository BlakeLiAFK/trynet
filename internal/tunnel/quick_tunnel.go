// quick_tunnel.go 是快速隧道模式的 API 客户端：不需要账号或认证，
// 一次调用换一条随机域名的临时隧道凭据。跟 cloudflare_api.go 是对称的
// 两套东西——那边是固定域名模式的 API 封装，这边是快速隧道模式的。
package tunnel

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"time"
)

const quickTunnelAPI = "https://api.trycloudflare.com/tunnel"

// APIClient 是所有"管理面"HTTP 调用共用的客户端：这个包向 trycloudflare 申请
// 快速隧道用它，cfapi 调 Cloudflare 管理 API 也用它。会自动识别 https_proxy
// 之类的环境变量。
//
// 跟隧道数据面的连接（proxy.go 里那套手写 CONNECT/socks5）是两回事，互不影响——
// 数据面要连边缘的 7844 端口，很多 http 代理转发不了，所以才另起炉灶。
//
// 放在这个包而不是 cfapi：依赖方向是 cfapi 依赖 tunnel（cfapi 产出 tunnel.Info），
// 反过来会成环。为这 4 行配置单开一个包不值当。
var APIClient = &http.Client{
	Timeout:   30 * time.Second,
	Transport: &http.Transport{Proxy: http.ProxyFromEnvironment},
}

// StoreFileName 是快速隧道凭据存档的文件名，落在当前工作目录（os.Getwd()，
// 不是 -dir 指定的分享目录）。文件服务默认会挡掉所有点开头的路径段
// （见 fileserver.go 的 blockSensitive），防止它被当成静态文件下发到公网。
const StoreFileName = ".trynet.json"

// Info 是 trycloudflare 下发的一次性隧道凭据
type Info struct {
	ID         string `json:"id"`
	Hostname   string `json:"hostname"`
	AccountTag string `json:"account_tag"`
	Secret     []byte `json:"secret"` // 服务端是 base64，encoding/json 会自动解码
}

// RequestQuick 向 trycloudflare 申请一条临时隧道，不需要任何账号或认证
func RequestQuick(ctx context.Context) (*Info, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, quickTunnelAPI, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "trynet")

	resp, err := APIClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("api returned %s", resp.Status)
	}

	var body struct {
		Success bool  `json:"success"`
		Result  *Info `json:"result"`
		Errors  []any `json:"errors"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return nil, err
	}
	if !body.Success || body.Result == nil {
		return nil, fmt.Errorf("api reported failure: %v", body.Errors)
	}
	return body.Result, nil
}

// storePath 返回存档文件在当前工作目录下的绝对路径
func storePath() (string, error) {
	dir, err := os.Getwd()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, StoreFileName), nil
}

// loadTunnelStore 读取当前目录下的存档凭据。文件不存在、读不出来、内容
// 不是合法 JSON、或者关键字段缺失，统一当成"没有存档"处理，返回 error
// 让调用方转去申请新隧道——文件很可能只是被手工改坏了，不值得拿这些
// 错误打扰用户。
func loadTunnelStore() (*Info, error) {
	path, err := storePath()
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var info Info
	if err := json.Unmarshal(data, &info); err != nil {
		return nil, err
	}
	if info.ID == "" || info.Hostname == "" || len(info.Secret) == 0 {
		return nil, fmt.Errorf("tunnel store missing required fields")
	}
	return &info, nil
}

// SaveStore 把凭据写进当前目录的存档文件，权限 0600——里面有 tunnel secret，
// 拿到它就能顶掉隧道、把域名指向别处。
func SaveStore(info *Info) error {
	path, err := storePath()
	if err != nil {
		return err
	}
	data, err := json.MarshalIndent(info, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o600)
}

// AcquireQuick 决定这次启动用哪份快速隧道凭据：forceNew、或者没有能
// 解析的存档时，走 refresh 申请一条新的；否则直接复用存档，不调 API。
// 第二个返回值说明这次是不是新申请的（调用方只在新申请时提示存档路径）。
func AcquireQuick(ctx context.Context, forceNew bool, refresh func(context.Context) (*Info, error)) (*Info, bool, error) {
	if !forceNew {
		if stored, err := loadTunnelStore(); err == nil {
			return stored, false, nil
		}
	}
	fetched, err := refresh(ctx)
	if err != nil {
		return nil, false, err
	}
	return fetched, true, nil
}
