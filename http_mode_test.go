package channel

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func signFor(secret, timestamp string) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(timestamp + "\n" + secret))
	return base64.StdEncoding.EncodeToString(mac.Sum(nil))
}

func httpBody(msgID, text string) []byte {
	data, _ := json.Marshal(map[string]any{
		"conversationId":   "cid-w",
		"conversationType": "1",
		"msgId":            msgID,
		"senderStaffId":    "staff-1",
		"sessionWebhook":   "",
		"text":             map[string]string{"content": text},
		"isInAtList":       true,
		"msgtype":          "text",
	})
	return data
}

func TestHTTPModeVerifySign(t *testing.T) {
	secret := "sec"
	now := time.Now()
	ts := strconv.FormatInt(now.UnixMilli(), 10)
	tsSec := strconv.FormatInt(now.Unix(), 10)

	if err := verifyHTTPSign(secret, ts, signFor(secret, ts), time.Hour, now); err != nil {
		t.Fatalf("valid ms timestamp should pass: %v", err)
	}
	if err := verifyHTTPSign(secret, tsSec, signFor(secret, tsSec), time.Hour, now); err != nil {
		t.Fatalf("valid s timestamp should pass: %v", err)
	}
	if err := verifyHTTPSign(secret, ts, signFor("wrong", ts), time.Hour, now); err == nil || !strings.Contains(err.Error(), "signature mismatch") {
		t.Fatalf("wrong secret should fail, got %v", err)
	}
	old := strconv.FormatInt(now.Add(-2*time.Hour).UnixMilli(), 10)
	if err := verifyHTTPSign(secret, old, signFor(secret, old), time.Hour, now); err == nil || !strings.Contains(err.Error(), "tolerance") {
		t.Fatalf("stale timestamp should fail, got %v", err)
	}
	if err := verifyHTTPSign(secret, old, signFor(secret, old), 0, now); err != nil {
		t.Fatalf("tolerance<=0 disables window check, got %v", err)
	}
}

// 验签通过 → 走完整管线；重试重推由 DedupCache 幂等吸收。
func TestHTTPModeHandleAndDedup(t *testing.T) {
	_, srv := newFakeAPI(t)
	cfg := testConfig(srv.URL, srv.URL)
	cfg.ClientSecret = "sec"
	cfg.Transport = TransportHTTP
	ch := New(cfg)
	var calls int32
	ch.OnMessage(func(ctx context.Context, msg *IncomingMessage, reply Reply) error {
		atomic.AddInt32(&calls, 1)
		if msg.Text != "hi" {
			t.Errorf("unexpected text %q", msg.Text)
		}
		return nil
	})

	ts := strconv.FormatInt(time.Now().UnixMilli(), 10)
	sign := signFor("sec", ts)
	body := httpBody("w-1", "hi")
	if err := ch.HandleHTTPCallback(context.Background(), body, ts, sign); err != nil {
		t.Fatalf("valid http callback should dispatch: %v", err)
	}
	if err := ch.HandleHTTPCallback(context.Background(), body, ts, sign); err != nil {
		t.Fatalf("duplicate http callback should not error: %v", err)
	}
	if n := atomic.LoadInt32(&calls); n != 1 {
		t.Fatalf("handler calls = %d, want 1 (dedup)", n)
	}

	if err := ch.HandleHTTPCallback(context.Background(), body, ts, "bad-sign"); err == nil || !strings.Contains(err.Error(), "signature") {
		t.Fatalf("bad sign should error, got %v", err)
	}
	if err := ch.HandleHTTPCallback(context.Background(), []byte("{bad"), ts, sign); err == nil {
		t.Fatalf("bad payload should error")
	}
}

// HTTP 适配层：401/200 语义。
func TestHTTPCallbackHandler(t *testing.T) {
	_, srv := newFakeAPI(t)
	cfg := testConfig(srv.URL, srv.URL)
	cfg.ClientSecret = "sec"
	ch := New(cfg)
	ch.OnMessage(func(ctx context.Context, msg *IncomingMessage, reply Reply) error { return nil })
	h := ch.HTTPCallbackHandler()

	ts := strconv.FormatInt(time.Now().UnixMilli(), 10)
	req := httptest.NewRequest(http.MethodPost, "/callback", strings.NewReader(string(httpBody("w-2", "hi"))))
	req.Header.Set("timestamp", ts)
	req.Header.Set("sign", signFor("sec", ts))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("valid request = %d, want 200 (body: %s)", rec.Code, rec.Body.String())
	}

	req2 := httptest.NewRequest(http.MethodPost, "/callback", strings.NewReader(string(httpBody("w-3", "hi"))))
	req2.Header.Set("timestamp", ts)
	req2.Header.Set("sign", "bad")
	rec2 := httptest.NewRecorder()
	h.ServeHTTP(rec2, req2)
	if rec2.Code != http.StatusUnauthorized {
		t.Fatalf("bad sign = %d, want 401", rec2.Code)
	}
}
