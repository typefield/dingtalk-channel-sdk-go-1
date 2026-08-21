package channel

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

// E7：卡片回调帧 → OnCardAction 分发（outTrackId/userId/dataContent 归一化）。
func TestCardActionDispatch(t *testing.T) {
	_, srv := newFakeAPI(t)
	ch := New(testConfig(srv.URL, srv.URL))

	var mu sync.Mutex
	var got []CardAction
	ch.OnCardAction(func(ctx context.Context, a *CardAction, r Reply) error {
		mu.Lock()
		got = append(got, *a)
		mu.Unlock()
		return nil
	})

	data, _ := json.Marshal(map[string]any{
		"outTrackId":  "card_123",
		"userId":      "u-1",
		"dataContent": map[string]any{"action": "confirm"},
	})
	frame := &frame{
		Type:    "CALLBACK",
		Headers: map[string]string{"topic": topicCardInstanceCB, "messageId": "m-c1"},
		Data:    string(data),
	}
	ch.dispatchFrame(context.Background(), frame)

	mu.Lock()
	defer mu.Unlock()
	if len(got) != 1 {
		t.Fatalf("card action handler calls = %d, want 1", len(got))
	}
	if got[0].OutTrackID != "card_123" || got[0].UserID != "u-1" {
		t.Fatalf("normalized fields wrong: %+v", got[0])
	}
	if !strings.Contains(string(got[0].DataContent), "confirm") {
		t.Fatalf("dataContent not passed: %s", got[0].DataContent)
	}
}

// E7：注册 OnCardAction 后，open 请求订阅列表应包含卡片 topic。
func TestCardTopicSubscription(t *testing.T) {
	var lastBody []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		lastBody, _ = io.ReadAll(r.Body)
		writeJSON(w, map[string]any{})
	}))
	defer srv.Close()

	ch := New(Config{ClientID: "ding-test", ClientSecret: "s", APIBase: srv.URL})
	if ch.conn.wantCardTopic() {
		t.Fatal("card topic should not be subscribed before OnCardAction")
	}
	ch.OnCardAction(func(ctx context.Context, a *CardAction, r Reply) error { return nil })
	if !ch.conn.wantCardTopic() {
		t.Fatal("card topic not subscribed after OnCardAction")
	}

	// 直接调 open 验证订阅列表（open 会因 endpoint 为空失败，但请求体已捕获）。
	conn := newStreamConn(ch.conn.cfg, ch.conn.httpc, nil)
	conn.cardTopicWanted = true
	_, _, _ = conn.open(context.Background())
	if !strings.Contains(string(lastBody), topicCardInstanceCB) {
		t.Fatalf("open request subscriptions missing card topic: %s", lastBody)
	}
}

// E9：媒体上传（OAPI gettoken + multipart /media/upload，字段名 media）。
func TestUploadMedia(t *testing.T) {
	var gotTokenQuery, gotUploadQuery, gotContentType string
	var gotFileBytes []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/v1.0/robot/robotInfo":
			writeJSON(w, map[string]any{"robotCode": "ding-test", "robotName": "TestBot"})
		case r.URL.Path == "/v1.0/oauth2/accessToken":
			// 新的 token 端点
			writeJSON(w, map[string]any{"accessToken": "new-tok", "expireIn": 7200})
			gotTokenQuery = r.URL.RawQuery
		case strings.HasSuffix(r.URL.Path, "/gettoken"):
			gotTokenQuery = r.URL.RawQuery
			writeJSON(w, map[string]any{"errcode": 0, "access_token": "oapi-tok", "expires_in": 7200})
		case strings.HasSuffix(r.URL.Path, "/media/upload"):
			gotUploadQuery = r.URL.RawQuery
			gotContentType = r.Header.Get("Content-Type")
			file, _, err := r.FormFile("media")
			if err != nil {
				t.Errorf("multipart field 'media' missing: %v", err)
				w.WriteHeader(400)
				return
			}
			gotFileBytes, _ = io.ReadAll(file)
			writeJSON(w, map[string]any{"errcode": 0, "media_id": "@MEDIA_123", "type": "image"})
		default:
			t.Errorf("unexpected call %s", r.URL.Path)
			w.WriteHeader(404)
		}
	}))
	defer srv.Close()

	ch := New(Config{ClientID: "ding-test", ClientSecret: "s", APIBase: srv.URL, OapiBase: srv.URL})
	var uploaded *MediaUploadResult
	done := make(chan struct{})
	ch.OnMessage(func(ctx context.Context, m *IncomingMessage, r Reply) error {
		var err error
		uploaded, err = r.UploadMedia(ctx, "image", "a.jpg", "", []byte("fake-jpeg-bytes"))
		if err != nil {
			t.Errorf("UploadMedia: %v", err)
		}
		close(done)
		return nil
	})
	ch.dispatchFrame(context.Background(), botFrame("m-1", "b-1", "hi", srv.URL+"/webhook"))
	<-done

	if !strings.Contains(gotTokenQuery, "appkey=ding-test") {
		t.Fatalf("gettoken query wrong: %s", gotTokenQuery)
	}
	if !strings.Contains(gotUploadQuery, "access_token=oapi-tok") || !strings.Contains(gotUploadQuery, "type=image") {
		t.Fatalf("upload query wrong: %s", gotUploadQuery)
	}
	if !strings.HasPrefix(gotContentType, "multipart/form-data") {
		t.Fatalf("content-type not multipart: %s", gotContentType)
	}
	if string(gotFileBytes) != "fake-jpeg-bytes" {
		t.Fatalf("uploaded bytes wrong: %q", gotFileBytes)
	}
	if uploaded == nil || uploaded.MediaID != "MEDIA_123" {
		t.Fatalf("mediaId should be cleaned of leading @: %+v", uploaded)
	}
	if uploaded.DownloadURL != "https://down.dingtalk.com/media/MEDIA_123" {
		t.Fatalf("DownloadURL wrong: %s", uploaded.DownloadURL)
	}
}

// 主动发消息：单聊 batchSend / 群聊 groupMessages/send（含 @）。
func TestProactiveSend(t *testing.T) {
	var mu sync.Mutex
	var calls []struct {
		path string
		body map[string]any
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		mu.Lock()
		calls = append(calls, struct {
			path string
			body map[string]any
		}{r.URL.Path, body})
		mu.Unlock()
		if strings.HasSuffix(r.URL.Path, "/oauth2/accessToken") {
			writeJSON(w, map[string]any{"accessToken": "tok", "expireIn": 7200})
			return
		}
		writeJSON(w, map[string]any{})
	}))
	defer srv.Close()

	ch := New(testConfig(srv.URL, srv.URL))
	ctx := context.Background()

	// 单聊 → oToMessages/batchSend
	if err := ch.SendText(ctx, SendTarget{UserID: "staff-1"}, "hello"); err != nil {
		t.Fatalf("SendText dm: %v", err)
	}
	// 群聊 + @ → groupMessages/send
	err := ch.SendMarkdown(ctx, SendTarget{
		ConversationID: "cid-g",
		AtUserIds:      []string{"u1", "u2"},
		AtAll:          false,
	}, "", "# hello")
	if err != nil {
		t.Fatalf("SendMarkdown group: %v", err)
	}
	// 非法目标
	if err := ch.SendText(ctx, SendTarget{}, "x"); err == nil {
		t.Fatal("empty target should fail")
	}

	mu.Lock()
	defer mu.Unlock()
	if len(calls) != 3 { // token + 两次发送
		t.Fatalf("calls = %d, want 3", len(calls))
	}
	if calls[1].path != "/v1.0/robot/oToMessages/batchSend" {
		t.Fatalf("dm path = %s", calls[1].path)
	}
	if ids, _ := calls[1].body["userIds"].([]any); len(ids) != 1 || ids[0] != "staff-1" {
		t.Fatalf("dm userIds = %v", calls[1].body["userIds"])
	}
	if mp, _ := calls[1].body["msgParam"].(string); mp == "" {
		t.Fatalf("msgParam should be stringified JSON: %v", calls[1].body["msgParam"])
	}
	if calls[2].path != "/v1.0/robot/groupMessages/send" {
		t.Fatalf("group path = %s", calls[2].path)
	}
	if at, _ := calls[2].body["atUserIds"].([]any); len(at) != 2 {
		t.Fatalf("group atUserIds = %v", calls[2].body["atUserIds"])
	}
	if calls[2].body["openConversationId"] != "cid-g" {
		t.Fatalf("group conversation = %v", calls[2].body["openConversationId"])
	}
}

// 效果
func TestTrailingFlush(t *testing.T) {
	f, srv := newFakeAPI(t)
	cfg := testConfig(srv.URL, srv.URL)
	cfg.StreamThrottle = 200 * time.Millisecond
	ch := New(cfg)

	done := make(chan struct{})
	ch.OnMessage(func(ctx context.Context, m *IncomingMessage, r Reply) error {
		s, _ := r.Stream(ctx)
		_ = s.Append("first")  // 立即刷（inputing+streaming）
		_ = s.Append("chunk2") // 落入节流窗口 → 应安排 trailing flush
		// 不调用 Finish：等待 trailing flush 自己把 chunk2 刷出去
		go func() {
			deadline := time.Now().Add(2 * time.Second)
			for time.Now().Before(deadline) {
				f.mu.Lock()
				hit := false
				for _, st := range f.streams {
					if c, _ := st["content"].(string); c == "firstchunk2" {
						hit = true
					}
				}
				f.mu.Unlock()
				if hit {
					close(done)
					return
				}
				time.Sleep(20 * time.Millisecond)
			}
		}()
		return nil
	})
	ch.dispatchFrame(context.Background(), botFrame("m-1", "b-1", "hi", srv.URL+"/webhook"))

	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("trailing flush did not deliver in-window content")
	}
}

// 真机发现的协议修正回归：单聊投递必须用 imRobotOpenDeliverModel（dws 生产真源），
// 且 HTTP 200 内 success:false 必须判为失败（不再静默降级误报成功）。
func TestDMDeliverFieldAndBusinessCheck(t *testing.T) {
	f, srv := newFakeAPI(t)

	// 记录投递体 + 单聊消息帧
	var mu sync.Mutex
	var deliverBodies []map[string]any
	f2 := srv
	_ = f2
	orig := srv.Config.Handler
	srv.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1.0/card/instances/deliver" {
			var b map[string]any
			_ = json.NewDecoder(r.Body).Decode(&b)
			mu.Lock()
			deliverBodies = append(deliverBodies, b)
			mu.Unlock()
		}
		orig.ServeHTTP(w, r)
	})

	// ① 单聊（conversationType=1）投递字段
	cfg := testConfig(srv.URL, srv.URL)
	ch := New(cfg)
	var cardOK bool
	done := make(chan struct{})
	ch.OnMessage(func(ctx context.Context, m *IncomingMessage, r Reply) error {
		s, _ := r.Stream(ctx)
		cardOK = s.CardDelivered()
		close(done)
		return nil
	})
	dmData, _ := json.Marshal(map[string]any{
		"conversationId": "cid-dm", "conversationType": "1", "msgId": "b-dm",
		"senderStaffId": "staff-dm", "sessionWebhook": srv.URL + "/webhook",
		"text": map[string]string{"content": "hi"},
	})
	ch.dispatchFrame(context.Background(), &frame{
		Type: "CALLBACK", Headers: map[string]string{"topic": topicBotMessage, "messageId": "m-dm"},
		Data: string(dmData),
	})
	<-done
	mu.Lock()
	dm := deliverBodies[len(deliverBodies)-1]
	mu.Unlock()
	if _, ok := dm["imRobotOpenDeliverModel"]; !ok {
		t.Fatalf("DM deliver 必须含 imRobotOpenDeliverModel: %v", dm)
	}
	if _, bad := dm["imRobotOpenSpaceModel"]; bad {
		t.Fatalf("DM deliver 不应再含 imRobotOpenSpaceModel: %v", dm)
	}
	if !cardOK {
		t.Fatal("DM card should be delivered")
	}

	// ② 业务级失败：HTTP 200 + {"success":false} → CardDelivered 必须为 false
	f.mu.Lock()
	f.businessFail = true
	f.mu.Unlock()
	ch2 := New(cfg)
	var cardOK2 bool
	done2 := make(chan struct{})
	ch2.OnMessage(func(ctx context.Context, m *IncomingMessage, r Reply) error {
		s, _ := r.Stream(ctx)
		cardOK2 = s.CardDelivered()
		_ = s.Finish("x")
		close(done2)
		return nil
	})
	ch2.dispatchFrame(context.Background(), &frame{
		Type: "CALLBACK", Headers: map[string]string{"topic": topicBotMessage, "messageId": "m-dm2"},
		Data: string(dmData),
	})
	<-done2
	if cardOK2 {
		t.Fatal("business failure (success:false in 200) must degrade, not report delivered")
	}
}

// connector 同款防线：看门狗强制收口孤儿卡；Abort 显式中止；错误冷却防刷屏。
func TestWatchdogForceFinish(t *testing.T) {
	f, srv := newFakeAPI(t)
	cfg := testConfig(srv.URL, srv.URL)
	cfg.CardWatchdog = 100 * time.Millisecond
	ch := New(cfg)

	streamerCh := make(chan CardStreamer, 1)
	ch.OnMessage(func(ctx context.Context, m *IncomingMessage, r Reply) error {
		s, _ := r.Stream(ctx)
		_ = s.Append("partial") // 有帧但故意不 Finish → 看门狗应强制收口
		streamerCh <- s
		return nil
	})
	ch.dispatchFrame(context.Background(), botFrame("m-1", "b-1", "hi", srv.URL+"/webhook"))
	s := <-streamerCh

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if s.(interface{ CardDelivered() bool }).CardDelivered() {
			// 等收口标志：实例里 flowStatus=FINISHED
			f.mu.Lock()
			done := false
			for _, c := range f.instances {
				if strings.Contains(c, "partial") {
					done = true
				}
			}
			f.mu.Unlock()
			if done {
				break
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	f.mu.Lock()
	finished := false
	for _, c := range f.instances {
		if strings.Contains(c, "partial") {
			finished = true
		}
	}
	f.mu.Unlock()
	if !finished {
		t.Fatal("watchdog did not force-finish the stale card")
	}
}

func TestAbortSetsFailed(t *testing.T) {
	_, srv := newFakeAPI(t)
	ch := New(testConfig(srv.URL, srv.URL))
	done := make(chan struct{})
	ch.OnMessage(func(ctx context.Context, m *IncomingMessage, r Reply) error {
		s, _ := r.Stream(ctx)
		_ = s.Append("to-be-aborted")
		if err := s.Abort(); err != nil {
			t.Errorf("Abort: %v", err)
		}
		if err := s.Finish("late"); err != nil {
			t.Errorf("Finish after Abort should be no-op nil, got %v", err)
		}
		close(done)
		return nil
	})
	ch.dispatchFrame(context.Background(), botFrame("m-1", "b-1", "hi", srv.URL+"/webhook"))
	<-done
}

// 过期消息丢弃。
func TestStaleMessageDropped(t *testing.T) {
	_, srv := newFakeAPI(t)
	cfg := testConfig(srv.URL, srv.URL)
	cfg.StaleMessageWindow = time.Minute
	ch := New(cfg)
	calls := 0
	ch.OnMessage(func(ctx context.Context, m *IncomingMessage, r Reply) error { calls++; return nil })

	// 构造 2 小时前的消息
	data, _ := json.Marshal(map[string]any{
		"conversationId": "cid-1", "conversationType": "1", "msgId": "b-old",
		"createAt": time.Now().Add(-2 * time.Hour).UnixMilli(),
		"text":     map[string]string{"content": "old"}, "sessionWebhook": srv.URL + "/webhook",
	})
	ch.dispatchFrame(context.Background(), &frame{
		Type: "CALLBACK", Headers: map[string]string{"topic": topicBotMessage, "messageId": "m-old"}, Data: string(data),
	})
	// 新消息正常处理
	ch.dispatchFrame(context.Background(), botFrame("m-new", "b-new", "hi", srv.URL+"/webhook"))
	if calls != 1 {
		t.Fatalf("stale should be dropped: calls=%d", calls)
	}
}

// 超长文本 newline 感知分片。
func TestTextChunking(t *testing.T) {
	f, srv := newFakeAPI(t)
	cfg := testConfig(srv.URL, srv.URL)
	cfg.TextChunkLimit = 20
	ch := New(cfg)
	ch.OnMessage(func(ctx context.Context, m *IncomingMessage, r Reply) error {
		return r.Text(ctx, strings.Repeat("a", 10)+"\n"+strings.Repeat("b", 10)+"\n"+strings.Repeat("c", 10))
	})
	ch.dispatchFrame(context.Background(), botFrame("m-1", "b-1", "hi", srv.URL+"/webhook"))

	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.webhook) < 2 {
		t.Fatalf("expected chunked sends, got %d", len(f.webhook))
	}
	total := 0
	for _, w := range f.webhook {
		mp, _ := w["msgParam"].(string)
		var p struct {
			Content string `json:"content"`
		}
		_ = json.Unmarshal([]byte(mp), &p)
		if lr := len([]rune(p.Content)); lr > 20 {
			t.Fatalf("chunk exceeds limit: %d", lr)
		}
		total += len([]rune(p.Content))
	}
	if total != 32 { // 10+1+10+1+10
		t.Fatalf("chunked content lost: total=%d", total)
	}
}
