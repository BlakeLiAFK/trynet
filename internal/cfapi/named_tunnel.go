// named_tunnel.go 负责"固定域名"模式的编排：把 cloudflare_api.go 里那些单个
// API 调用串成一条完整流程（解析账号 -> 解析 zone -> 找/建 tunnel -> 取 token ->
// 对好 DNS），并管理这条流程建出来的云端资源，支撑 Teardown 清理和 token 刷新。
// cloudflare_api.go 只管单次 API 调用，这个文件不碰 HTTP 细节。
package cfapi

import (
	"context"
	"errors"
	"fmt"
	"time"

	"trynet/internal/tunnel"
)

// Handle 记着 SetupNamedTunnel 建好/复用的资源 id，
// 用来支撑 -Teardown（精确删除）和注册被拒绝时的 token 刷新。
type Handle struct {
	token       string
	accountID   string
	zoneID      string
	tunnelID    string
	domain      string
	dnsRecordID string // 只有这次自己新建的记录才有值；发现已存在的记录不会被删

	// createdTunnel 只有这次自己新建的 tunnel 才是 true；cfFindTunnel 复用到的
	// tunnel 可能是用户上一次运行留下的、甚至是挪作别的用途，Teardown 时绝不能
	// 顺手删掉——那会让对应的 DNS 记录变成悬空指向，下次运行直接卡死。
	// 这个字段和上面的 dnsRecordID 是同一种保护，对称地套在 tunnel 上。
	createdTunnel bool
}

// SetupNamedTunnel 编排"解析身份 -> 找/建 tunnel -> 取 token -> 对好 DNS"整条链路。
// 返回的 info 可以直接喂给现有的 serve()/run()。
// 如果 tunnel 建成功了，但后面取 token 或建 DNS 失败，这个 tunnel 不会变成孤儿：
// 隧道名是由域名确定性推导出来的（sanitizeTunnelName），下次重跑会按名字找到
// 并复用它，而不是又建一个新的。这是有意的自愈设计，不需要额外的清理逻辑。
//
// 限制：正因为隧道名是由域名确定性推导出来的，同一个域名不能同时在两台机器上跑
// trynet。cfGetTunnelToken 拿到 token 后，边缘注册连接时会带 ReplaceExisting=true，
// 两边会反复互相把对方的连接踢掉，陷入无限重连。同一个域名同一时间只应该有
// 一个 trynet 实例在跑；快速隧道模式（不带 -domain）每次隧道都是新建的，没有这个问题。
func SetupNamedTunnel(ctx context.Context, token, accountHint, domain string) (*tunnel.Info, *Handle, error) {
	accountID, err := cfResolveAccount(ctx, token, accountHint)
	if err != nil {
		return nil, nil, fmt.Errorf("resolve account: %w", err)
	}

	zone, err := cfResolveZone(ctx, token, domain)
	if err != nil {
		return nil, nil, fmt.Errorf("resolve zone: %w", err)
	}

	name := sanitizeTunnelName(domain)
	existing, err := cfFindTunnel(ctx, token, accountID, name)
	if err != nil {
		return nil, nil, fmt.Errorf("find tunnel: %w", err)
	}
	tunnelID := ""
	createdTunnel := false
	if existing != nil {
		tunnelID = existing.ID
	} else {
		created, err := cfCreateTunnel(ctx, token, accountID, name)
		if err != nil {
			return nil, nil, fmt.Errorf("create tunnel: %w", err)
		}
		tunnelID = created.ID
		createdTunnel = true
	}

	info, err := cfGetTunnelToken(ctx, token, accountID, tunnelID)
	if err != nil {
		return nil, nil, fmt.Errorf("get tunnel token: %w", err)
	}
	info.Hostname = domain

	handle := &Handle{
		token: token, accountID: accountID, zoneID: zone.ID, tunnelID: tunnelID, domain: domain,
		createdTunnel: createdTunnel,
	}

	target := tunnelID + tunnelDNSSuffix
	record, err := cfFindDNSRecord(ctx, token, zone.ID, domain)
	if err != nil {
		return nil, nil, fmt.Errorf("find dns record: %w", err)
	}
	switch {
	case record != nil && record.Type == dnsRecordType && record.Content == target:
		// 已经指对了，什么都不用做
	case record != nil:
		// 记录存在但不满足上面那条：可能类型压根不是 CNAME（比如已经有一条 A 记录
		// 占着这个名字），也可能是 CNAME 但指向别处。两种情况 Cloudflare 建记录时
		// 都只会甩一个生硬的错误码回来，这里统一给出人能看懂的提示，把记录类型和
		// 当前内容都带上，让用户一眼看出到底是什么挡在前面。
		return nil, nil, fmt.Errorf("dns record for %s already exists as a %s record pointing to %q, expected a CNAME to %q; fix it manually or use a different domain",
			domain, record.Type, record.Content, target)
	default:
		created, err := cfCreateDNSRecord(ctx, token, zone.ID, domain, target)
		if err != nil {
			return nil, nil, fmt.Errorf("create dns record: %w", err)
		}
		handle.dnsRecordID = created.ID
	}

	return info, handle, nil
}

// RefreshToken 只重新取一次 connector token，不重复走建 tunnel/建 DNS 那一套——
// 那些资源已经在了。run() 在注册被拒绝时调这个。
func (h *Handle) RefreshToken(ctx context.Context) (*tunnel.Info, error) {
	info, err := cfGetTunnelToken(ctx, h.token, h.accountID, h.tunnelID)
	if err != nil {
		return nil, err
	}
	info.Hostname = h.domain
	return info, nil
}

// Teardown 删掉这次自动创建的云端资源：DNS 记录和 tunnel，都只删自己新建的那种，
// 复用到的一律跳过（见 dnsRecordID / createdTunnel 上的注释）。
// tunnel 删除紧跟在连接刚断开之后调用，边缘那边有时候还没反应过来这条连接已经关了，
// 会报"active connections"之类的错误，所以这里做几次重试，隔几秒再试。
//
// 90 秒的预算明显大于单次 API 调用 30 秒的超时（见 tunnel.APIClient），这样万一第一次
// cfDeleteTunnel 就撞上客户端超时，后面还有余量把 3 次重试真正跑起来，
// 不会被第一次调用瞬间吃光整个预算导致重试形同虚设。
func (h *Handle) Teardown() error {
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	var errs []error
	if h.dnsRecordID != "" {
		if err := cfDeleteDNSRecord(ctx, h.token, h.zoneID, h.dnsRecordID); err != nil {
			errs = append(errs, fmt.Errorf("delete dns record: %w", err))
		}
	}

	if h.createdTunnel {
		var lastErr error
		for attempt := 0; attempt < 3; attempt++ {
			if lastErr = cfDeleteTunnel(ctx, h.token, h.accountID, h.tunnelID); lastErr == nil {
				break
			}
			// 最后一次尝试失败后没必要再等，直接退出循环
			if attempt == 2 || !sleepCtx(ctx, 2*time.Second) {
				break
			}
		}
		if lastErr != nil {
			errs = append(errs, fmt.Errorf("delete tunnel: %w", lastErr))
		}
	}

	return errors.Join(errs...)
}

// sleepCtx 等待 d 或 ctx 被取消，返回 false 表示是被取消打断的。
//
// 和 tunnel 包里那个同名函数是两份一样的代码，这是故意的：它跟隧道没有任何
// 关系，为了省 7 行去 import 隧道包拿一个 sleep，读的人会先愣一下"删 DNS
// 记录的重试为什么要从隧道包拿东西"。
func sleepCtx(ctx context.Context, d time.Duration) bool {
	select {
	case <-ctx.Done():
		return false
	case <-time.After(d):
		return true
	}
}
