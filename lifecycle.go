package channel

import "sync"

// LifecycleHooks 管理连接生命周期钩子。
type LifecycleHooks struct {
	mu sync.RWMutex

	onReady        []func()
	onError        []func(err error)
	onReconnecting []func()
	onReconnected  []func()
	onDisconnected []func()
}

func newLifecycleHooks() *LifecycleHooks {
	return &LifecycleHooks{}
}

// OnReady 注册连接就绪回调。
func (h *LifecycleHooks) OnReady(fn func()) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.onReady = append(h.onReady, fn)
}

// OnError 注册连接错误回调。
func (h *LifecycleHooks) OnError(fn func(err error)) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.onError = append(h.onError, fn)
}

// OnReconnecting 注册重连中回调。
func (h *LifecycleHooks) OnReconnecting(fn func()) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.onReconnecting = append(h.onReconnecting, fn)
}

// OnReconnected 注册重连成功回调。
func (h *LifecycleHooks) OnReconnected(fn func()) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.onReconnected = append(h.onReconnected, fn)
}

// OnDisconnected 注册断开连接回调。
func (h *LifecycleHooks) OnDisconnected(fn func()) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.onDisconnected = append(h.onDisconnected, fn)
}

// FireReady 触发所有 onReady 回调。
func (h *LifecycleHooks) FireReady() {
	h.mu.RLock()
	defer h.mu.RUnlock()
	for _, fn := range h.onReady {
		fn()
	}
}

// FireError 触发所有 onError 回调。
func (h *LifecycleHooks) FireError(err error) {
	h.mu.RLock()
	defer h.mu.RUnlock()
	for _, fn := range h.onError {
		fn(err)
	}
}

// FireReconnecting 触发所有 onReconnecting 回调。
func (h *LifecycleHooks) FireReconnecting() {
	h.mu.RLock()
	defer h.mu.RUnlock()
	for _, fn := range h.onReconnecting {
		fn()
	}
}

// FireReconnected 触发所有 onReconnected 回调。
func (h *LifecycleHooks) FireReconnected() {
	h.mu.RLock()
	defer h.mu.RUnlock()
	for _, fn := range h.onReconnected {
		fn()
	}
}

// FireDisconnected 触发所有 onDisconnected 回调。
func (h *LifecycleHooks) FireDisconnected() {
	h.mu.RLock()
	defer h.mu.RUnlock()
	for _, fn := range h.onDisconnected {
		fn()
	}
}
