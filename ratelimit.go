package channel

import (
	"sync"
	"time"
)

// tokenBucket 全局令牌桶（卡片 API，SPEC §6）。
// waitFor 在无令牌时阻塞等待；backoff 触发 2s 全局暂停。
type tokenBucket struct {
	rate       float64 // tokens per second
	mu         sync.Mutex
	tokens     float64
	lastRefill time.Time
	backoffEnd time.Time
}

func newTokenBucket(rate float64) *tokenBucket {
	return &tokenBucket{
		rate:       rate,
		tokens:     rate,
		lastRefill: time.Now(),
	}
}

func (b *tokenBucket) refillLocked(now time.Time) {
	elapsed := now.Sub(b.lastRefill).Seconds()
	if elapsed > 0 {
		if b.tokens += elapsed * b.rate; b.tokens > b.rate {
			b.tokens = b.rate
		}
		b.lastRefill = now
	}
}

// waitFor 取一个令牌，必要时阻塞。返回实际等待时长。
func (b *tokenBucket) waitFor(ctxDone <-chan struct{}) (time.Duration, error) {
	start := time.Now()
	for {
		b.mu.Lock()
		now := time.Now()
		if now.Before(b.backoffEnd) {
			sleep := b.backoffEnd.Sub(now)
			b.mu.Unlock()
			if err := sleepCtx(ctxDone, sleep); err != nil {
				return 0, err
			}
			continue
		}
		b.refillLocked(now)
		if b.tokens >= 1 {
			b.tokens--
			b.mu.Unlock()
			return time.Since(start), nil
		}
		need := time.Duration((1 - b.tokens) / b.rate * float64(time.Second))
		b.mu.Unlock()
		if err := sleepCtx(ctxDone, need); err != nil {
			return 0, err
		}
	}
}

// triggerBackoff 清空令牌并暂停 defaultQPSBackoff。
func (b *tokenBucket) triggerBackoff() {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.backoffEnd = time.Now().Add(defaultQPSBackoff)
	b.tokens = 0
	b.lastRefill = b.backoffEnd
}

func sleepCtx(done <-chan struct{}, d time.Duration) error {
	if d <= 0 {
		d = time.Millisecond
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-t.C:
		return nil
	case <-done:
		return contextCanceled
	}
}

var contextCanceled = errCanceled{}

type errCanceled struct{}

func (errCanceled) Error() string { return "context canceled" }
