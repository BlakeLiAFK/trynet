// cloudflare_api.go 是 Cloudflare 管理 API 的最小封装：账号/zone 解析、
// named tunnel 的查找/创建、DNS 记录的查找/创建/删除。
// 只用来给 -domain 模式建好云端资源，拿到 token 之后就交回给 main.go 里
// 现成的连接逻辑，这个文件不碰隧道协议本身。
package cfapi

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"trynet/internal/tunnel"
)

// cfAPIBase 是个变量而不是常量，方便测试时指向本地 httptest.Server
var cfAPIBase = "https://api.cloudflare.com/client/v4"

// Cloudflare 的资源路径集中在这里。CF 改过隧道接口的命名（早期 /tunnels，
// 现在 /cfd_tunnel），集中一处的话以后再改只动这个块。
func pathAccounts() string { return "/accounts" }
func pathZones() string    { return "/zones" }

func pathTunnels(accountID string) string          { return "/accounts/" + accountID + "/cfd_tunnel" }
func pathTunnel(accountID, tunnelID string) string { return pathTunnels(accountID) + "/" + tunnelID }
func pathTunnelToken(accountID, tunnelID string) string {
	return pathTunnel(accountID, tunnelID) + "/token"
}

func pathDNSRecords(zoneID string) string          { return "/zones/" + zoneID + "/dns_records" }
func pathDNSRecord(zoneID, recordID string) string { return pathDNSRecords(zoneID) + "/" + recordID }

const (
	// tunnelDNSSuffix 是隧道在 DNS 上的固定落点，CNAME 要指向 <tunnel-id> 加这个后缀
	tunnelDNSSuffix = ".cfargotunnel.com"
	// tunnelConfigSrc 表示隧道配置由 Cloudflare 侧托管，这样才能随时重新取 token
	tunnelConfigSrc = "cloudflare"
	// dnsRecordType 隧道路由只能用 CNAME
	dnsRecordType = "CNAME"
)

// cfResponse 是 Cloudflare API 统一的响应外壳
type cfResponse[T any] struct {
	Success    bool         `json:"success"`
	Errors     []cfAPIError `json:"errors"`
	Result     T            `json:"result"`
	ResultInfo cfResultInfo `json:"result_info"`
}

// cfResultInfo 是 list 接口的分页信息
type cfResultInfo struct {
	Page       int `json:"page"`
	PerPage    int `json:"per_page"`
	Count      int `json:"count"`
	TotalCount int `json:"total_count"`
	TotalPages int `json:"total_pages"`
}

type cfAPIError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type cfAccount struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

type cfZone struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

type cfTunnel struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

type cfDNSRecord struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Type    string `json:"type"`
	Content string `json:"content"`
	Proxied bool   `json:"proxied"`
}

// cfDoFull 发一个请求并返回完整的响应外壳，翻页时要读里面的 result_info。
func cfDoFull[T any](ctx context.Context, method, path, token string, body any) (cfResponse[T], error) {
	var zero cfResponse[T]

	var reqBody io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return zero, fmt.Errorf("cloudflare api: encode request body: %w", err)
		}
		reqBody = bytes.NewReader(b)
	}

	req, err := http.NewRequestWithContext(ctx, method, cfAPIBase+path, reqBody)
	if err != nil {
		return zero, fmt.Errorf("cloudflare api: build request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := tunnel.APIClient.Do(req)
	if err != nil {
		return zero, fmt.Errorf("cloudflare api: send request: %w", err)
	}
	defer resp.Body.Close()

	var parsed cfResponse[T]
	if err := json.NewDecoder(resp.Body).Decode(&parsed); err != nil {
		return zero, fmt.Errorf("cloudflare api: decode response: %w (http %s)", err, resp.Status)
	}
	if !parsed.Success {
		return zero, &cfError{Status: resp.StatusCode, Errors: parsed.Errors}
	}
	return parsed, nil
}

// cfError 带着 Cloudflare 原样返回的错误码，调用方可以据此区分
// "资源不存在"这类可以忽略的情况和真正的失败（比如权限不够）。
type cfError struct {
	Status int
	Errors []cfAPIError
}

func (e *cfError) Error() string {
	return "cloudflare api: " + formatCFErrors(e.Errors, e.Status)
}

// hasCode 判断这个错误里是否包含指定的 Cloudflare 错误码
func (e *cfError) hasCode(code int) bool {
	for _, item := range e.Errors {
		if item.Code == code {
			return true
		}
	}
	return false
}

// cfNotFoundCodes 是 Cloudflare 表示"资源不存在"的错误码。
// 目前只确认了 DNS 记录的 81044；其它资源如果发现新的码，往这里加。
var cfNotFoundCodes = []int{81044}

// IsNotFound 判断一个错误是不是 Cloudflare 在说"这东西不存在"。
// 删除操作用它来保持幂等：本来就没有，等于目标已经达成。
// 同时认 HTTP 404，因为不是所有资源的"不存在"都用同一个错误码。
func IsNotFound(err error) bool {
	var e *cfError
	if !errors.As(err, &e) {
		return false
	}
	if e.Status == http.StatusNotFound {
		return true
	}
	for _, code := range cfNotFoundCodes {
		if e.hasCode(code) {
			return true
		}
	}
	return false
}

// cfDo 发一个 Cloudflare API 请求，把 result 字段解到 T 里。
// API 返回 success=false 时，把 errors 数组原样拼成一句话报出去，
// 用户照着这句话就知道该去补哪个 token 权限，不用来回猜一层包装过的 Go error。
func cfDo[T any](ctx context.Context, method, path, token string, body any) (T, error) {
	resp, err := cfDoFull[T](ctx, method, path, token, body)
	if err != nil {
		var zero T
		return zero, err
	}
	return resp.Result, nil
}

// cfList 把一个分页 list 接口翻完。Cloudflare 默认每页 20 条、上限 50 条，
// 只看第一页的话，域名或账号多的用户会被漏掉，还会收到一个"找不到"的误导性报错。
func cfList[T any](ctx context.Context, base, token string, q url.Values) ([]T, error) {
	const perPage = 50
	if q == nil {
		q = url.Values{}
	}
	var all []T
	for page := 1; ; page++ {
		q.Set("page", strconv.Itoa(page))
		q.Set("per_page", strconv.Itoa(perPage))
		resp, err := cfDoFull[[]T](ctx, http.MethodGet, cfPath(base, q), token, nil)
		if err != nil {
			return nil, err
		}
		all = append(all, resp.Result...)
		// total_pages 为 0 表示服务端没返回分页信息，拿到一页就收手；
		// 空页也停，免得服务端行为异常时死循环
		if resp.ResultInfo.TotalPages <= page || len(resp.Result) == 0 {
			return all, nil
		}
	}
}

// cfPath 往路径上拼 query 参数，转义交给标准库。
func cfPath(base string, q url.Values) string {
	if len(q) == 0 {
		return base
	}
	return base + "?" + q.Encode()
}

func formatCFErrors(errs []cfAPIError, status int) string {
	if len(errs) == 0 {
		return fmt.Sprintf("http %d", status)
	}
	parts := make([]string, len(errs))
	for i, e := range errs {
		parts[i] = fmt.Sprintf("%s (code %d)", e.Message, e.Code)
	}
	return strings.Join(parts, "; ")
}

// sanitizeTunnelName 把域名变成 Cloudflare tunnel 名字允许的格式：
// 只能小写字母、数字、连字符，首尾不能是连字符，1-63 字符。
// files.example.com -> trynet-files-example-com
// 前缀 trynet- 是故意的：一是让隧道在 dashboard 列表里一眼就能认出是 trynet 建的，
// 二是让同一个域名每次都推导出同一个名字，这样 cfFindTunnel 才能找到并复用它。
// 注意这只是命名习惯，不是删除时的安全校验——Teardown 删不删一个 tunnel，
// 看的是 Handle.createdTunnel，不看名字前缀。
func sanitizeTunnelName(domain string) string {
	const prefix = "trynet-"
	var b strings.Builder
	b.WriteString(prefix)
	prevDash := true // 前缀已经以连字符结尾了，避免开头非法字符生成双连字符
	for _, r := range strings.ToLower(domain) {
		switch {
		case r >= 'a' && r <= 'z' || r >= '0' && r <= '9':
			b.WriteRune(r)
			prevDash = false
		default:
			if !prevDash {
				b.WriteByte('-')
				prevDash = true
			}
		}
	}
	name := strings.TrimRight(b.String(), "-")
	const maxLen = 63
	if len(name) > maxLen {
		name = strings.TrimRight(name[:maxLen], "-")
	}
	return name
}

// 账号列表也是分页接口，必须用 cfList 翻完，否则账号多的用户会被漏掉
func cfListAccounts(ctx context.Context, token string) ([]cfAccount, error) {
	return cfList[cfAccount](ctx, pathAccounts(), token, nil)
}

// cfResolveAccount 返回要用的 account id：给了 hint 就直接用，不发请求；
// 没给就要求 token 只能访问恰好一个 account，多个就报错列出来让用户用 -account-id 选。
func cfResolveAccount(ctx context.Context, token, hint string) (string, error) {
	if hint != "" {
		return hint, nil
	}
	accounts, err := cfListAccounts(ctx, token)
	if err != nil {
		return "", err
	}
	switch len(accounts) {
	case 0:
		return "", fmt.Errorf("this token cannot access any Cloudflare account; check the token's Account permissions")
	case 1:
		return accounts[0].ID, nil
	default:
		names := make([]string, len(accounts))
		for i, a := range accounts {
			names[i] = fmt.Sprintf("%s (%s)", a.Name, a.ID)
		}
		return "", fmt.Errorf("this token can access multiple accounts, pick one with -account-id:\n  %s",
			strings.Join(names, "\n  "))
	}
}

// zone 列表默认每页只有 20 条，域名多的账号必须翻页才能找全，
// 不然 cfResolveZone 会误报"没有 zone 覆盖这个域名"
func cfListZones(ctx context.Context, token string) ([]cfZone, error) {
	return cfList[cfZone](ctx, pathZones(), token, nil)
}

// cfResolveZone 在 token 能访问的 zone 里找 domain 的最长后缀匹配。
// domain=files.sub.example.com，zone 列表里同时有 example.com 和 sub.example.com，
// 用更具体的 sub.example.com。
func cfResolveZone(ctx context.Context, token, domain string) (cfZone, error) {
	zones, err := cfListZones(ctx, token)
	if err != nil {
		return cfZone{}, err
	}
	var best cfZone
	for _, z := range zones {
		if domain != z.Name && !strings.HasSuffix(domain, "."+z.Name) {
			continue
		}
		if len(z.Name) > len(best.Name) {
			best = z
		}
	}
	if best.ID == "" {
		return cfZone{}, fmt.Errorf("no zone in this token's account covers %q; check the domain spelling or the token's Zone permissions", domain)
	}
	return best, nil
}

type cfCreateTunnelBody struct {
	Name      string `json:"name"`
	ConfigSrc string `json:"config_src"`
}

// cfFindTunnel 按名字查一个存量 tunnel，不存在返回 (nil, nil)，不当错误处理。
func cfFindTunnel(ctx context.Context, token, accountID, name string) (*cfTunnel, error) {
	q := url.Values{"name": {name}, "is_deleted": {"false"}}
	tunnels, err := cfList[cfTunnel](ctx, pathTunnels(accountID), token, q)
	if err != nil {
		return nil, err
	}
	for _, t := range tunnels {
		if t.Name == name {
			return &t, nil
		}
	}
	return nil, nil
}

// cfCreateTunnel 建一个"远程管理"的 tunnel（config_src=cloudflare），
// 不自己生成 tunnel_secret，交给 Cloudflare 管理——这样才能用下面 cfGetTunnelToken
// 随时重新要 token，不用在本地存密钥。
func cfCreateTunnel(ctx context.Context, token, accountID, name string) (cfTunnel, error) {
	body := cfCreateTunnelBody{Name: name, ConfigSrc: tunnelConfigSrc}
	return cfDo[cfTunnel](ctx, http.MethodPost, pathTunnels(accountID), token, body)
}

// cfGetTunnelToken 取一个 tunnel 的 connector token 并解成现有的 tunnel.Info 结构，
// 新建的、复用的都走这一个函数。这个接口对已存在的 tunnel 也能随时重新调用。
func cfGetTunnelToken(ctx context.Context, token, accountID, tunnelID string) (*tunnel.Info, error) {
	raw, err := cfDo[string](ctx, http.MethodGet, pathTunnelToken(accountID, tunnelID), token, nil)
	if err != nil {
		return nil, err
	}
	decoded, err := base64.StdEncoding.DecodeString(raw)
	if err != nil {
		return nil, fmt.Errorf("decode tunnel token: %w", err)
	}
	var t struct {
		AccountTag string `json:"a"`
		TunnelID   string `json:"t"`
		Secret     []byte `json:"s"`
	}
	if err := json.Unmarshal(decoded, &t); err != nil {
		return nil, fmt.Errorf("parse tunnel token: %w", err)
	}
	return &tunnel.Info{ID: t.TunnelID, AccountTag: t.AccountTag, Secret: t.Secret}, nil
}

type cfCreateDNSBody struct {
	Type    string `json:"type"`
	Name    string `json:"name"`
	Content string `json:"content"`
	Proxied bool   `json:"proxied"`
}

// cfFindDNSRecord 按名字查一条存量 DNS 记录，不限类型，不存在返回 (nil, nil)。
// 故意不按 type=CNAME 过滤：调用方要能发现同名的 A/AAAA 之类的冲突记录，
// 才能给出"这个名字已经被占用"的统一友好提示，而不是让 Cloudflare 建记录时
// 用一个生硬的错误码把用户挡回来。
// 同名同类型的记录理论上可能有多条，但 DNS 规范里同一个名字下的 CNAME 必须唯一
// （不能和其它记录共存），所以这里直接取 records[0]，不用再挑一条。
func cfFindDNSRecord(ctx context.Context, token, zoneID, name string) (*cfDNSRecord, error) {
	q := url.Values{"name": {name}}
	records, err := cfList[cfDNSRecord](ctx, pathDNSRecords(zoneID), token, q)
	if err != nil {
		return nil, err
	}
	if len(records) == 0 {
		return nil, nil
	}
	return &records[0], nil
}

// cfCreateDNSRecord 建一条指向 tunnel 的 CNAME。必须 proxied=true（橙云）——
// 灰云的话这条记录只是普通 DNS 解析，压根连不到 Cloudflare 边缘的隧道路由上。
func cfCreateDNSRecord(ctx context.Context, token, zoneID, domain, target string) (cfDNSRecord, error) {
	body := cfCreateDNSBody{Type: dnsRecordType, Name: domain, Content: target, Proxied: true}
	return cfDo[cfDNSRecord](ctx, http.MethodPost, pathDNSRecords(zoneID), token, body)
}

// 删除是幂等的：资源本来就不存在，说明目标状态已经达成，不该当成失败。
// Cloudflare 删除不存在的 DNS 记录会返回 81044 而不是静默成功。
func cfDeleteDNSRecord(ctx context.Context, token, zoneID, recordID string) error {
	_, err := cfDo[map[string]any](ctx, http.MethodDelete, pathDNSRecord(zoneID, recordID), token, nil)
	if IsNotFound(err) {
		return nil
	}
	return err
}

// 同样保持幂等：tunnel 已经不存在（比如手动删过、或者 Teardown 跑了两次），
// 目标状态已经达成，不该报错。
func cfDeleteTunnel(ctx context.Context, token, accountID, tunnelID string) error {
	_, err := cfDo[map[string]any](ctx, http.MethodDelete, pathTunnel(accountID, tunnelID), token, nil)
	if IsNotFound(err) {
		return nil
	}
	return err
}
