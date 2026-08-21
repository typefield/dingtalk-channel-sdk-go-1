package channel

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

// E8 + SPEC §2：假网关验证 open→wss→帧→ACK 全链路与 SYSTEM ping。
func TestStreamEndToEnd(t *testing.T) {
	var ackMu sync.Mutex
	var gotAcks []frameAck
	ackCh := make(chan struct{}, 4)

	upgrader := websocket.Upgrader{}
	wsSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1.0/gateway/connections/open" {
			writeJSON(w, map[string]any{
				"endpoint": "ws" + strings.TrimPrefix(wsURLBase(w, r), "http"),
				"ticket":   "t-1",
			})
			return
		}
		// Handle background HTTP endpoints that the client calls.
		if r.URL.Path == "/v1.0/oauth2/accessToken" {
			writeJSON(w, map[string]any{"accessToken": "tok-e2e", "expireIn": 7200})
			return
		}
		if r.URL.Path == "/v1.0/robot/robotInfo" {
			writeJSON(w, map[string]any{"robotCode": "ding-test", "robotName": "TestBot"})
			return
		}
		c, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			t.Errorf("upgrade: %v", err)
			return
		}
		defer c.Close()
		go func() {
			for {
				var ack frameAck
				if err := c.ReadJSON(&ack); err != nil {
					return
				}
				ackMu.Lock()
				gotAcks = append(gotAcks, ack)
				ackMu.Unlock()
				ackCh <- struct{}{}
			}
		}()

		// 下发一条机器人消息 + SYSTEM ping。
		data, _ := json.Marshal(map[string]any{"text": map[string]string{"content": "ping"}, "msgId": "b-9", "conversationId": "cid", "isInAtList": true})
		_ = c.WriteJSON(frame{
			Type:    "CALLBACK",
			Headers: map[string]string{"topic": topicBotMessage, "messageId": "m-9", "contentType": "application/json"},
			Data:    string(data),
		})
		_ = c.WriteJSON(frame{
			Type:    "SYSTEM",
			Headers: map[string]string{"topic": "ping", "messageId": "m-ping"},
			Data:    "keepalive",
		})
		// 收齐 2 个 ACK 或超时。
		deadline := time.After(3 * time.Second)
		for i := 0; i < 2; i++ {
			select {
			case <-ackCh:
			case <-deadline:
				t.Errorf("timed out waiting for ack %d", i)
				return
			}
		}
	}))
	defer wsSrv.Close()

	ch := New(Config{
		ClientID: "ding-test", ClientSecret: "s",
		APIBase: wsSrv.URL, CardQPS: 100, StreamThrottle: time.Millisecond,
	})
	got := make(chan string, 1)
	ch.OnMessage(func(ctx context.Context, m *IncomingMessage, r Reply) error {
		got <- m.Text
		return nil
	})

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	go func() { _ = ch.Start(ctx) }()

	select {
	case txt := <-got:
		if txt != "ping" {
			t.Fatalf("unexpected text %q", txt)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("no message delivered")
	}

	// 等待两个 ACK 均写出。
	deadline := time.After(2 * time.Second)
	ackMu.Lock()
	n := len(gotAcks)
	ackMu.Unlock()
	for n < 2 {
		select {
		case <-ackCh:
		case <-deadline:
			t.Fatalf("expected 2 acks, got %d", n)
		}
		ackMu.Lock()
		n = len(gotAcks)
		ackMu.Unlock()
	}
	foundPong := false
	ackMu.Lock()
	snapshot := append([]frameAck(nil), gotAcks...)
	ackMu.Unlock()
	for _, a := range snapshot {
		if a.Code != 200 || a.Headers["messageId"] == "" {
			t.Fatalf("bad ack: %+v", a)
		}
		if a.Data == "keepalive" {
			foundPong = true
		}
	}
	if !foundPong {
		t.Fatal("SYSTEM ping not ponged with echoed data")
	}
	ch.Close()
}

// wsURLBase 从请求反推本服务地址（endpoint 需指向自身）。
func wsURLBase(w http.ResponseWriter, r *http.Request) string {
	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}
	// 通过 httptest server 的 Host 头构造。
	return scheme + "://" + r.Host
}

func TestMarkdownNormalization(t *testing.T) {
	cases := []struct{ in, want string }{
		{"a\nb", "a<br>b"},
		{"a\n\nb", "a\n\nb"},
		{"```\nx\ny\n```", "```\nx\ny\n```"},
		{"- a\n- b", "- a\n- b"},
		{"# T\nbody", "# T<br>body"},
		{"body\n# T", "body\n# T"},
		{"a\n> q1\n> q2\nb", "a<br>> q1<br>q2<br>b"},
		{"x\n| a | b |\n| -- | -- |\n| 1 | 2 |", "x\n\n| a | b |\n| -- | -- |\n| 1 | 2 |"},
	}
	for _, c := range cases {
		if got := normalizeForCard(c.in); got != c.want {
			t.Errorf("normalizeForCard(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// E8 回归：服务端下发 SYSTEM/disconnect 后必须重连（而非整体退出）。
func TestStreamReconnectOnServerDisconnect(t *testing.T) {
	upgrader := websocket.Upgrader{}
	var connections int32
	sawMessage := make(chan struct{}, 1)

	wsSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1.0/gateway/connections/open" {
			writeJSON(w, map[string]any{"endpoint": "ws" + strings.TrimPrefix(wsURLBase(w, r), "http"), "ticket": "t-1"})
			return
		}
		c, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer c.Close()
		n := atomic.AddInt32(&connections, 1)
		if n == 1 {
			// 第一条连接：立即下发 disconnect
			_ = c.WriteJSON(frame{Type: "SYSTEM", Headers: map[string]string{"topic": "disconnect", "messageId": "m-d"}})
			// 等客户端 ACK 后关闭
			var ack frameAck
			_ = c.SetReadDeadline(time.Now().Add(3 * time.Second))
			_ = c.ReadJSON(&ack)
			return
		}
		// 第二条连接：正常消息
		data, _ := json.Marshal(map[string]any{"text": map[string]string{"content": "after-reconnect"}, "msgId": "b-r", "conversationId": "cid", "isInAtList": true})
		_ = c.WriteJSON(frame{Type: "CALLBACK", Headers: map[string]string{"topic": topicBotMessage, "messageId": "m-r"}, Data: string(data)})
		var ack frameAck
		_ = c.SetReadDeadline(time.Now().Add(3 * time.Second))
		_ = c.ReadJSON(&ack)
	}))
	defer wsSrv.Close()

	ch := New(Config{ClientID: "ding-test", ClientSecret: "s", APIBase: wsSrv.URL, CardQPS: 100, StreamThrottle: time.Millisecond})
	got := make(chan string, 1)
	ch.OnMessage(func(ctx context.Context, m *IncomingMessage, r Reply) error {
		got <- m.Text
		close(sawMessage)
		return nil
	})
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	go func() { _ = ch.Start(ctx) }()

	select {
	case txt := <-got:
		if txt != "after-reconnect" {
			t.Fatalf("unexpected text %q", txt)
		}
	case <-time.After(7 * time.Second):
		t.Fatal("not reconnected after SYSTEM/disconnect")
	}
	if n := atomic.LoadInt32(&connections); n < 2 {
		t.Fatalf("expected >=2 connections, got %d", n)
	}
	ch.Close()
}
