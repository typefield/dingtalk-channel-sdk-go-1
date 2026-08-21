package channel

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sync"
	"time"
)

// BotIdentityCacheConfig 控制机器人身份缓存行为。
type BotIdentityCacheConfig struct {
	// TTL 缓存有效期（默认 30 分钟）
	TTL time.Duration
	// MinRefreshInterval 刷新失败后的最小重试间隔（默认 1 分钟）
	MinRefreshInterval time.Duration
}

// botIdentityProvider 机器人身份提供者（带缓存）。
type botIdentityProvider struct {
	cfg    *Config
	httpc  *http.Client
	tokens *tokenProvider

	cacheCfg BotIdentityCacheConfig

	mu            sync.Mutex
	identity      *BotIdentity
	fetchedAt     time.Time
	lastFailureAt time.Time
}

func newBotIdentityProvider(cfg *Config, httpc *http.Client, tokens *tokenProvider) *botIdentityProvider {
	return &botIdentityProvider{
		cfg:    cfg,
		httpc:  httpc,
		tokens: tokens,
		cacheCfg: BotIdentityCacheConfig{
			TTL:                30 * time.Minute,
			MinRefreshInterval: 1 * time.Minute,
		},
	}
}

// isCacheFreshLocked 检查缓存是否新鲜（调用者必须持有锁）。
func (p *botIdentityProvider) isCacheFreshLocked() bool {
	if p.identity == nil {
		return false
	}
	if p.cacheCfg.TTL <= 0 {
		return true
	}
	return time.Since(p.fetchedAt) < p.cacheCfg.TTL
}

// isCacheFresh 检查缓存是否新鲜（线程安全）。
func (p *botIdentityProvider) isCacheFresh() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.isCacheFreshLocked()
}

// shouldThrottleRefreshLocked 检查是否应该限制刷新频率（调用者必须持有锁）。
func (p *botIdentityProvider) shouldThrottleRefreshLocked(now time.Time) bool {
	if p.lastFailureAt.IsZero() {
		return false
	}
	if p.cacheCfg.MinRefreshInterval <= 0 {
		return false
	}
	return now.Sub(p.lastFailureAt) < p.cacheCfg.MinRefreshInterval
}

// Get 获取机器人身份（带缓存）。
func (p *botIdentityProvider) Get(ctx context.Context) *BotIdentity {
	// 快速路径：缓存新鲜则直接返回
	p.mu.Lock()
	if p.isCacheFreshLocked() {
		id := p.identity
		p.mu.Unlock()
		return id
	}
	p.mu.Unlock()

	// 慢路径：需要刷新
	p.mu.Lock()
	defer p.mu.Unlock()

	// Double-check after acquiring lock
	if p.isCacheFreshLocked() {
		return p.identity
	}

	now := time.Now()
	if p.shouldThrottleRefreshLocked(now) {
		return p.identity
	}

	identity, err := p.fetch(ctx)
	if err != nil {
		p.lastFailureAt = now
		if p.identity != nil {
			// 有旧缓存，返回旧的（降级）
			p.cfg.debugf("failed to refresh bot identity, using stale cache: %v", err)
			return p.identity
		}
		p.cfg.debugf("failed to fetch bot identity: %v", err)
		return nil
	}

	p.identity = identity
	p.fetchedAt = time.Now()
	p.lastFailureAt = time.Time{}
	return identity
}

// fetch 从 API 获取机器人身份信息。
func (p *botIdentityProvider) fetch(ctx context.Context) (*BotIdentity, error) {
	token, err := p.tokens.Get(ctx)
	if err != nil {
		return nil, fmt.Errorf("get token: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, "GET", p.cfg.APIBase+"/v1.0/robot/robotInfo", nil)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("x-acs-dingtalk-access-token", token)

	resp, err := p.httpc.Do(req)
	if err != nil {
		return nil, fmt.Errorf("do request: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read body: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("robotInfo API failed: status=%d body=%s", resp.StatusCode, string(body))
	}

	var result struct {
		RobotCode string `json:"robotCode"`
		RobotName string `json:"robotName"`
		Avatar    string `json:"avatar"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, fmt.Errorf("unmarshal response: %w", err)
	}

	// 如果 API 返回空值，使用配置中的默认值
	if result.RobotCode == "" {
		result.RobotCode = p.cfg.ClientID
	}
	if result.RobotName == "" {
		result.RobotName = "Bot"
	}

	return &BotIdentity{
		RobotCode: result.RobotCode,
		RobotName: result.RobotName,
		Avatar:    result.Avatar,
	}, nil
}
