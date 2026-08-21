package safety

import (
	"sync"
	"time"
)

// ProcessingLock 短时 TTL 内存锁，防止同一事件并发处理。
type ProcessingLock struct {
	mu     sync.Mutex
	locks  map[string]int64 // id -> expireAt (Unix ms)
	ttlMs  int64
	ticker *time.Ticker
	stopCh chan struct{}
}

func NewProcessingLock(ttl time.Duration, sweepInterval time.Duration) *ProcessingLock {
	pl := &ProcessingLock{
		locks:  make(map[string]int64),
		ttlMs:  ttl.Milliseconds(),
		ticker: time.NewTicker(sweepInterval),
		stopCh: make(chan struct{}),
	}
	go pl.sweepLoop()
	return pl
}

// Acquire 获取锁，成功返回 true，已被持有返回 false。
func (pl *ProcessingLock) Acquire(id string) bool {
	pl.mu.Lock()
	defer pl.mu.Unlock()

	now := time.Now().UnixMilli()
	exp, exists := pl.locks[id]
	if exists && exp > now {
		return false
	}
	pl.locks[id] = now + pl.ttlMs
	return true
}

// Release 释放锁。
func (pl *ProcessingLock) Release(id string) {
	pl.mu.Lock()
	defer pl.mu.Unlock()
	delete(pl.locks, id)
}

func (pl *ProcessingLock) sweepLoop() {
	for {
		select {
		case <-pl.ticker.C:
			pl.sweep()
		case <-pl.stopCh:
			pl.ticker.Stop()
			return
		}
	}
}

func (pl *ProcessingLock) sweep() {
	pl.mu.Lock()
	defer pl.mu.Unlock()

	now := time.Now().UnixMilli()
	for id, exp := range pl.locks {
		if exp <= now {
			delete(pl.locks, id)
		}
	}
}

func (pl *ProcessingLock) Dispose() {
	close(pl.stopCh)
	pl.mu.Lock()
	defer pl.mu.Unlock()
	pl.locks = make(map[string]int64)
}
