package channel

import (
	"context"
	"encoding/json"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func cardActionFrame(messageID, outTrackID string) *frame {
	data, _ := json.Marshal(map[string]any{
		"outTrackId":  outTrackID,
		"userId":      "u-1",
		"dataContent": map[string]any{"action": "confirm"},
	})
	return &frame{
		Type:    "CALLBACK",
		Headers: map[string]string{"topic": topicCardInstanceCB, "messageId": messageID},
		Data:    string(data),
	}
}

// TestCardActionDedup 同一卡片回调事件（相同 messageId）重复投递只处理一次。
func TestCardActionDedup(t *testing.T) {
	_, srv := newFakeAPI(t)
	cfg := testConfig(srv.URL, srv.URL)
	ch := New(cfg)
	defer ch.Close()

	var calls int32
	ch.OnCardAction(func(ctx context.Context, a *CardAction, r Reply) error {
		atomic.AddInt32(&calls, 1)
		return nil
	})

	ctx := context.Background()
	ch.dispatchFrame(ctx, cardActionFrame("m-c1", "card_1"))
	ch.dispatchFrame(ctx, cardActionFrame("m-c1", "card_1")) // 网关重复投递
	ch.dispatchFrame(ctx, cardActionFrame("m-c2", "card_1")) // 换投递 ID 重放

	if n := atomic.LoadInt32(&calls); n != 1 {
		t.Fatalf("card action calls = %d, want 1（两层去重）", n)
	}
}

// TestCardActionSerialPerCard 同一卡片（outTrackId）的多个动作必须串行执行。
func TestCardActionSerialPerCard(t *testing.T) {
	_, srv := newFakeAPI(t)
	cfg := testConfig(srv.URL, srv.URL)
	ch := New(cfg)
	defer ch.Close()

	var active, maxActive int32
	var mu sync.Mutex
	ch.OnCardAction(func(ctx context.Context, a *CardAction, r Reply) error {
		cur := atomic.AddInt32(&active, 1)
		mu.Lock()
		if cur > maxActive {
			maxActive = cur
		}
		mu.Unlock()
		time.Sleep(30 * time.Millisecond)
		atomic.AddInt32(&active, -1)
		return nil
	})

	ctx := context.Background()
	var wg sync.WaitGroup
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			ch.dispatchFrame(ctx, cardActionFrame("m-s"+string(rune('0'+i)), "card_same"))
		}(i)
	}
	wg.Wait()
	time.Sleep(50 * time.Millisecond) // 等待队列排空

	mu.Lock()
	defer mu.Unlock()
	if maxActive != 1 {
		t.Fatalf("max concurrent handlers on same card = %d, want 1（同卡片串行）", maxActive)
	}
}
