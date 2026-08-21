package channel

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"sync"
	"time"
)

// DefaultOapiBase 旧版 OAPI 域名（媒体上传走 OAPI，SPEC §9）。
const DefaultOapiBase = "https://oapi.dingtalk.com"

// oapiClient 旧版 OAPI：gettoken 缓存 + 媒体上传（对比官方 connector media/common.ts 移植）。
type oapiClient struct {
	cfg    *Config
	httpc  *http.Client
	oapiMu sync.Mutex
	token  string
	expiry time.Time
}

func newOapiClient(cfg *Config, httpc *http.Client) *oapiClient {
	return &oapiClient{cfg: cfg, httpc: httpc}
}

// MediaUploadResult 媒体上传结果。
type MediaUploadResult struct {
	MediaID     string `json:"mediaId"`    // 去掉 @ 的 mediaId
	RawMediaID  string `json:"rawMediaId"` // 原始带 @ 的 media_id（发文件消息 sampleFile 必须用这个）
	Type        string `json:"type"`
	CreatedAt   int64  `json:"created_at"`  // 钉钉返回 unix 时间戳（数字）
	DownloadURL string `json:"downloadUrl"` // down.dingtalk.com 媒体 URL（仅部分媒体可达，图片可靠送达走 SendFile）
}

// token 获取 OAPI access_token（GET /gettoken），过期前 60s 刷新。
func (o *oapiClient) getToken(ctx context.Context) (string, error) {
	o.oapiMu.Lock()
	defer o.oapiMu.Unlock()
	if o.token != "" && time.Now().Before(o.expiry.Add(-60*time.Second)) {
		return o.token, nil
	}
	q := url.Values{"appkey": {o.cfg.ClientID}, "appsecret": {o.cfg.ClientSecret}}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		o.cfg.OapiBase+"/gettoken?"+q.Encode(), nil)
	if err != nil {
		return "", err
	}
	resp, err := o.httpc.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	var out struct {
		ErrCode     int    `json:"errcode"`
		ErrMsg      string `json:"errmsg"`
		AccessToken string `json:"access_token"`
		ExpiresIn   int64  `json:"expires_in"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return "", err
	}
	if out.ErrCode != 0 || out.AccessToken == "" {
		return "", fmt.Errorf("oapi gettoken: errcode=%d errmsg=%s", out.ErrCode, out.ErrMsg)
	}
	o.token = out.AccessToken
	if out.ExpiresIn <= 0 {
		out.ExpiresIn = 7200
	}
	o.expiry = time.Now().Add(time.Duration(out.ExpiresIn) * time.Second)
	return o.token, nil
}

// UploadMedia 上传媒体文件（multipart，字段名 media），返回 mediaId（去前导 @）。
// mediaType: image | file | video | voice；contentType 空则按类型推断。
func (o *oapiClient) UploadMedia(ctx context.Context, mediaType, filename, contentType string, data []byte) (*MediaUploadResult, error) {
	token, err := o.getToken(ctx)
	if err != nil {
		return nil, err
	}
	if contentType == "" {
		if mediaType == "image" {
			contentType = "image/jpeg"
		} else {
			contentType = "application/octet-stream"
		}
	}

	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	fw, err := w.CreateFormFile("media", filename)
	if err != nil {
		return nil, err
	}
	if _, err := fw.Write(data); err != nil {
		return nil, err
	}
	_ = w.Close()

	q := url.Values{"access_token": {token}, "type": {mediaType}}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		o.cfg.OapiBase+"/media/upload?"+q.Encode(), &buf)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", w.FormDataContentType())
	resp, err := o.httpc.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	var out struct {
		ErrCode   int    `json:"errcode"`
		ErrMsg    string `json:"errmsg"`
		MediaID   string `json:"media_id"`
		Type      string `json:"type"`
		CreatedAt int64  `json:"created_at"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, err
	}
	if out.ErrCode != 0 {
		return nil, fmt.Errorf("media/upload: errcode=%d errmsg=%s", out.ErrCode, out.ErrMsg)
	}
	rawID := out.MediaID
	mediaID := rawID
	if len(mediaID) > 0 && mediaID[0] == '@' {
		mediaID = mediaID[1:] // 官方 connector 同款清理
	}
	return &MediaUploadResult{
		MediaID:     mediaID,
		RawMediaID:  rawID,
		Type:        out.Type,
		CreatedAt:   out.CreatedAt,
		DownloadURL: "https://down.dingtalk.com/media/" + mediaID,
	}, nil
}
