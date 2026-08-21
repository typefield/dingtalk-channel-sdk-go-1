package channel

import (
	"encoding/json"
	"context"
	"strings"
	"sync"
	"testing"
	"time"
)

// ── SSRF 白名单 ──

func TestSSRFAllowlist(t *testing.T) {
	ctx := context.Background()
	if err := AssertPublicURLWithAllowlist(ctx, "http://cdn.internal.corp/x", []string{"*.internal.corp"}); err != nil {
		t.Fatalf("wildcard allowlist should bypass: %v", err)
	}
	if err := AssertPublicURLWithAllowlist(ctx, "http://internal.corp/x", []string{"internal.corp"}); err != nil {
		t.Fatalf("exact allowlist should bypass: %v", err)
	}
	if err := AssertPublicURLWithAllowlist(ctx, "http://other.corp/x", []string{"*.internal.corp"}); err == nil {
		t.Fatal("non-listed host must stay blocked")
	}
	if err := AssertPublicURLWithAllowlist(ctx, "http://127.0.0.1/x", []string{"internal.corp"}); err == nil {
		t.Fatal("loopback must stay blocked")
	}
}

// ── 媒体批处理：合并资源 ──

func TestMediaBatchMergesResources(t *testing.T) {
	_, srv := newFakeAPI(t)
	cfg := testConfig(srv.URL, srv.URL)
	cfg.ChatQueue = &ChatQueueConfig{Enabled: true}
	cfg.MediaBatch = &MediaBatchConfig{Enabled: true, DelayMs: 30, MaxItems: 9}

	ch := New(cfg)
	defer ch.Close()

	var mu sync.Mutex
	var got [][]Resource
	ch.onBatch = func(ctx context.Context, batch *BatchedMessage) error {
		mu.Lock()
		got = append(got, batch.Message.Resources)
		mu.Unlock()
		return nil
	}

	ctx := context.Background()
	for i := 0; i < 2; i++ {
		data, _ := json.Marshal(map[string]any{
			"conversationId":   "cid-1",
			"conversationType": "2",
			"msgId":            "img-" + string(rune('0'+i)),
			"senderStaffId":    "staff-1",
			"sessionWebhook":   srv.URL + "/webhook",
			"isInAtList":       true,
			"msgtype":          "picture",
			"content":          map[string]string{"downloadCode": "code-" + string(rune('0'+i))},
		})
		ch.dispatchFrame(ctx, &frame{
			Type:    "CALLBACK",
			Headers: map[string]string{"topic": topicBotMessage, "messageId": "m-img" + string(rune('0'+i))},
			Data:    string(data),
		})
	}
	ch.chatQueueMgr.FlushAll(ctx)
	time.Sleep(50 * time.Millisecond)

	mu.Lock()
	defer mu.Unlock()
	if len(got) != 1 {
		t.Fatalf("expected 1 merged media batch, got %d", len(got))
	}
	if len(got[0]) != 2 {
		t.Fatalf("merged batch should carry 2 resources, got %d", len(got[0]))
	}
}

// ── 出站钩子 + 页脚 ──

func TestOutboundHooksAndFooter(t *testing.T) {
	f, srv := newFakeAPI(t)
	cfg := testConfig(srv.URL, srv.URL)
	var mu sync.Mutex
	var before []string
	var after []bool
	cfg.Outbound = &OutboundConfig{
		Footer: "由 AI 生成",
		BeforeSend: func(kind, target string, payload any) any {
			mu.Lock()
			before = append(before, kind)
			mu.Unlock()
			return payload
		},
		AfterSend: func(kind, target string, ok bool, errStr string) {
			mu.Lock()
			after = append(after, ok)
			mu.Unlock()
		},
	}
	ch := New(cfg)

	msg := &IncomingMessage{ConversationID: "cid-ob", SessionWebhook: srv.URL + "/webhook"}
	reply := &replier{msg: msg, cfg: &cfg, tokens: ch.tokens, cards: ch.cards, oapi: ch.oapi, httpc: ch.httpc}
	if err := reply.Text(context.Background(), "hello"); err != nil {
		t.Fatalf("reply text: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(before) != 1 || before[0] != "reply" {
		t.Fatalf("before_send should fire once for reply, got %v", before)
	}
	if len(after) != 1 || !after[0] {
		t.Fatalf("after_send should fire ok, got %v", after)
	}
	// 页脚应出现在 webhook 请求体
	if len(f.webhook) == 0 {
		t.Fatal("webhook not called")
	}
	mp, _ := f.webhook[0]["msgParam"].(string)
	if !strings.Contains(mp, "由 AI 生成") {
		t.Fatalf("footer missing in payload: %s", mp)
	}
}
