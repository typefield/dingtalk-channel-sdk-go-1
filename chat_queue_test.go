package channel

import (
	"context"
	"sync"
	"testing"
	"time"
)

// TestChatQueueBasic 测试基本的批处理和串行功能
func TestChatQueueBasic(t *testing.T) {
	_, srv := newFakeAPI(t)
	cfg := testConfig(srv.URL, srv.URL)
	cfg.DebugLog = t.Logf  // 启用调试日志
	
	// 启用 ChatQueue
	cfg.ChatQueue = &ChatQueueConfig{
		Enabled: true,
	}
	
	ch := New(cfg)
	defer ch.Close()

	var mu sync.Mutex
	var batches []*BatchedMessage
	
	ch.onBatch = func(ctx context.Context, batch *BatchedMessage) error {
		t.Logf("Batch received: %d source messages", len(batch.SourceIDs))
		mu.Lock()
		batches = append(batches, batch)
		mu.Unlock()
		return nil
	}

	ctx := context.Background()

	// 发送 3 条消息到同一会话（不调用 Start，直接 dispatch）
	for i := 1; i <= 3; i++ {
		f := botFrame(
			"m-"+string(rune('0'+i)),
			"msg-"+string(rune('0'+i)),
			"message",
			srv.URL+"/webhook",
		)
		ch.dispatchFrame(ctx, f)
	}

	// 手动 flush 并等待队列处理完成
	if ch.chatQueueMgr != nil {
		ch.chatQueueMgr.FlushAll(ctx)
	}
	time.Sleep(50 * time.Millisecond)

	mu.Lock()
	count := len(batches)
	mu.Unlock()

	if count != 1 {
		t.Errorf("expected 1 batch, got %d", count)
	}
}

// TestChatQueueSerial 测试 per-chat 串行队列
func TestChatQueueSerial(t *testing.T) {
	_, srv := newFakeAPI(t)
	cfg := testConfig(srv.URL, srv.URL)
	cfg.DebugLog = t.Logf
	
	// 启用 ChatQueue
	cfg.ChatQueue = &ChatQueueConfig{
		Enabled: true,
	}
	
	ch := New(cfg)
	defer ch.Close()

	var mu sync.Mutex
	var order []string
	var processing sync.Map
	
	ch.OnMessage(func(ctx context.Context, msg *IncomingMessage, r Reply) error {
		// 检查是否有并发处理
		if _, loaded := processing.LoadOrStore(msg.ConversationID, true); loaded {
			t.Errorf("concurrent processing detected for conversation %s", msg.ConversationID)
		}
		
		// 模拟处理时间
		time.Sleep(10 * time.Millisecond)
		
		mu.Lock()
		order = append(order, msg.MsgID)
		mu.Unlock()
		
		processing.Delete(msg.ConversationID)
		return nil
	})

	ctx := context.Background()

	// 快速发送 3 条消息（不调用 Start）
	for i := 1; i <= 3; i++ {
		f := botFrame(
			"m-"+string(rune('0'+i)),
			"msg-"+string(rune('0'+i)),
			"message",
			srv.URL+"/webhook",
		)
		ch.dispatchFrame(ctx, f)
	}

	// 手动 flush 并等待队列处理完成
	if ch.chatQueueMgr != nil {
		ch.chatQueueMgr.FlushAll(ctx)
	}
	time.Sleep(100 * time.Millisecond)

	mu.Lock()
	defer mu.Unlock()

	if len(order) != 3 {
		t.Errorf("expected 3 messages, got %d", len(order))
		return
	}
	
	// 验证顺序
	expected := []string{"msg-1", "msg-2", "msg-3"}
	for i, id := range expected {
		if order[i] != id {
			t.Errorf("message %d: expected %s, got %s", i, id, order[i])
		}
	}
}

// TestChatQueueDisabled 显式禁用 ChatQueue：消息不再合并，逐条经 OnBatch 投递
func TestChatQueueDisabled(t *testing.T) {
	_, srv := newFakeAPI(t)
	cfg := testConfig(srv.URL, srv.URL)

	// 显式禁用 ChatQueue → 直接分发，无批处理
	cfg.ChatQueue = &ChatQueueConfig{Enabled: false}

	ch := New(cfg)
	defer ch.Close()

	var mu sync.Mutex
	var batches []*BatchedMessage

	ch.onBatch = func(ctx context.Context, batch *BatchedMessage) error {
		mu.Lock()
		batches = append(batches, batch)
		mu.Unlock()
		return nil
	}

	ctx := context.Background()
	for i := 1; i <= 3; i++ {
		id := string(rune('0' + i))
		ch.dispatchFrame(ctx, botFrame("m-"+id, "msg-"+id, "message", srv.URL+"/webhook"))
	}

	time.Sleep(50 * time.Millisecond)

	mu.Lock()
	defer mu.Unlock()
	if len(batches) != 3 {
		t.Fatalf("expected 3 individual deliveries with queue disabled, got %d", len(batches))
	}
	for i, b := range batches {
		if len(b.SourceIDs) != 1 {
			t.Fatalf("batch %d should contain exactly 1 source message, got %d", i, len(b.SourceIDs))
		}
	}
}
