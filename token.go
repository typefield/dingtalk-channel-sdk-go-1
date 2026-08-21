package channel

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sync"
	"time"
)

// tokenProvider 获取并缓存 access token（过期前 60s 刷新，SPEC §8）。
type tokenProvider struct {
	cfg   *Config
	httpc *http.Client

	mu        sync.Mutex
	token     string
	expiresAt time.Time
}

func newTokenProvider(cfg *Config, httpc *http.Client) *tokenProvider {
	return &tokenProvider{cfg: cfg, httpc: httpc}
}

func (t *tokenProvider) Get(ctx context.Context) (string, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.token != "" && time.Now().Before(t.expiresAt.Add(-60*time.Second)) {
		return t.token, nil
	}

	body, _ := json.Marshal(map[string]string{
		"appKey":    t.cfg.ClientID,
		"appSecret": t.cfg.ClientSecret,
	})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		t.cfg.APIBase+"/v1.0/oauth2/accessToken", bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := t.httpc.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("accessToken: http %d: %s", resp.StatusCode, raw)
	}

	var out struct {
		AccessToken string `json:"accessToken"`
		ExpireIn    int64  `json:"expireIn"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return "", err
	}
	if out.AccessToken == "" {
		return "", fmt.Errorf("accessToken: empty token in response: %s", raw)
	}
	t.token = out.AccessToken
	if out.ExpireIn <= 0 {
		out.ExpireIn = 7200
	}
	t.expiresAt = time.Now().Add(time.Duration(out.ExpireIn) * time.Second)
	return t.token, nil
}
