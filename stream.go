package channel

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/rand"
	"net"
	"net/http"
	"net/url"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

// streamConn 管理 Stream 长连接生命周期：建连、订阅、心跳、重连、ACK（SPEC §2）。
type streamConn struct {
	cfg   *Config
	httpc *http.Client

	// onFrame 由 Channel 注入；返回值作为 ACK 的 data（空则用 {"success":true}）。
	onFrame func(ctx context.Context, f *frame) string
	// cardTopicWanted 为 true 时额外订阅卡片回调 topic。
	cardTopicWanted bool
	// hooks 生命周期钩子。
	hooks *LifecycleHooks

	mu       sync.Mutex
	conn     *websocket.Conn
	closed   bool
	stopOnce sync.Once
	stopCh   chan struct{}
	// connectedOnce 标记本次 runOnce 是否成功建立过连接（用于重连退避归零）
	connectedOnce bool
}

func newStreamConn(cfg *Config, httpc *http.Client, onFrame func(context.Context, *frame) string) *streamConn {
	return &streamConn{cfg: cfg, httpc: httpc, onFrame: onFrame, stopCh: make(chan struct{})}
}

func (s *streamConn) open(ctx context.Context) (endpoint, ticket string, err error) {
	subs := []subscription{
		{Type: string(subSystem), Topic: "ping"},
		{Type: string(subSystem), Topic: "disconnect"},
		{Type: string(subCallback), Topic: topicBotMessage},
	}
	if s.wantCardTopic() {
		subs = append(subs, subscription{Type: string(subCallback), Topic: topicCardInstanceCB})
	}
	reqBody, _ := json.Marshal(openConnectionRequest{
		ClientID:      s.cfg.ClientID,
		ClientSecret:  s.cfg.ClientSecret,
		Subscriptions: subs,
		UA:            UserAgent,
		LocalIP:       firstLanIP(),
	})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		s.cfg.APIBase+"/v1.0/gateway/connections/open", bytes.NewReader(reqBody))
	if err != nil {
		return "", "", err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", UserAgent)
	resp, err := s.httpc.Do(req)
	if err != nil {
		return "", "", err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return "", "", fmt.Errorf("gateway open: http %d: %s", resp.StatusCode, raw)
	}
	var out openConnectionResponse
	if err := json.Unmarshal(raw, &out); err != nil {
		return "", "", err
	}
	if out.Endpoint == "" || out.Ticket == "" {
		return "", "", errors.New("gateway open: empty endpoint/ticket")
	}
	return out.Endpoint, out.Ticket, nil
}

func (s *streamConn) wantCardTopic() bool { return s.cardTopicWanted }

// Run 阻塞运行：连接 → 读循环；断开时按配置重连（E8）。
func (s *streamConn) Run(ctx context.Context) error {
	attempt := 0
	firstConnect := true
	for {
		err := s.runOnce(ctx, firstConnect)
		if s.connectedOnce {
			// 成功建立过连接：退避计数归零，下次重连从最小间隔重新开始
			attempt = 0
		}
		s.connectedOnce = false
		firstConnect = false
		if s.isStopped() {
			return nil
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if !s.cfg.reconnectEnabled() {
			return err
		}
		// 触发重连钩子
		if s.hooks != nil {
			s.hooks.FireDisconnected()
			s.hooks.FireReconnecting()
		}
		delay := backoffDelay(attempt)
		attempt++
		s.cfg.debugf("stream disconnected (%v), reconnect in %v", err, delay)
		select {
		case <-time.After(delay):
		case <-s.stopCh:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

func (s *streamConn) runOnce(ctx context.Context, firstConnect bool) error {
	dialCtx, cancelDial := context.WithTimeout(ctx, 10*time.Second)
	defer cancelDial()

	endpoint, ticket, err := s.open(dialCtx)
	if err != nil {
		if s.hooks != nil {
			s.hooks.FireError(err)
		}
		return err
	}
	q := url.Values{"ticket": []string{ticket}}
	wssURL := endpoint + "?" + q.Encode()
	conn, _, err := websocket.DefaultDialer.DialContext(dialCtx, wssURL, nil)
	if err != nil {
		if s.hooks != nil {
			s.hooks.FireError(err)
		}
		return err
	}

	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		conn.Close()
		return nil
	}
	s.conn = conn
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		s.conn = nil
		s.mu.Unlock()
		conn.Close()
	}()

	s.cfg.debugf("stream connected")
	s.connectedOnce = true

	// 触发连接就绪钩子
	if s.hooks != nil {
		if firstConnect {
			s.hooks.FireReady()
		} else {
			s.hooks.FireReconnected()
		}
	}

	return s.readLoop(ctx, conn)
}

func (s *streamConn) readLoop(ctx context.Context, conn *websocket.Conn) error {
	readCh := make(chan []byte, 8)
	errCh := make(chan error, 1)
	pongCh := make(chan struct{}, 1)

	conn.SetPongHandler(func(string) error {
		select {
		case pongCh <- struct{}{}:
		default:
		}
		return nil
	})

	go func() {
		for {
			mt, data, err := conn.ReadMessage()
			if err != nil {
				errCh <- err
				return
			}
			if mt == websocket.TextMessage {
				readCh <- data
			}
		}
	}()

	idle := s.cfg.KeepAliveIdle
	for {
		timer := time.NewTimer(idle)
		select {
		case raw := <-readCh:
			timer.Stop()
			s.handleFrame(ctx, conn, raw)
		case err := <-errCh:
			timer.Stop()
			if s.hooks != nil {
				s.hooks.FireError(err)
			}
			return err
		case <-timer.C:
			if err := conn.WriteControl(websocket.PingMessage, nil, time.Now().Add(5*time.Second)); err != nil {
				return err
			}
			select {
			case <-pongCh:
			case <-time.After(DefaultPongWait):
				return errors.New("pong timeout")
			case <-errCh:
				return errors.New("connection error while waiting pong")
			}
		case <-s.stopCh:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

func (s *streamConn) handleFrame(ctx context.Context, conn *websocket.Conn, raw []byte) {
	var f frame
	if err := json.Unmarshal(raw, &f); err != nil {
		s.cfg.debugf("bad frame: %v", err)
		return
	}
	topic := f.topic()

	// SYSTEM ping：回 pong（data 原样回显）。
	if f.Type == string(subSystem) && topic == "ping" {
		ack := successAck(f.messageID(), f.Data)
		ack.Data = f.Data
		_ = conn.WriteJSON(ack)
		return
	}
	// SYSTEM disconnect：只关当前连接（不置 stopped），触发 Run 的重连分支（E8）。
	if f.Type == string(subSystem) && topic == "disconnect" {
		_ = conn.WriteJSON(successAck(f.messageID(), ""))
		go conn.Close()
		return
	}

	// ACK 先行（对齐官方 connector）：立即确认，业务处理异步进行，
	// 防止长任务期间服务端超时重投。重复投递由双层去重兜底（E6）。
	_ = conn.WriteJSON(successAck(f.messageID(), ""))
	if s.onFrame != nil {
		go s.onFrame(ctx, &f)
	}
}

// Close 停止连接与重连循环（幂等）。
func (s *streamConn) Close() {
	s.stopOnce.Do(func() {
		s.mu.Lock()
		s.closed = true
		conn := s.conn
		s.mu.Unlock()
		close(s.stopCh)
		if conn != nil {
			_ = conn.Close()
		}
	})
}

func (s *streamConn) isStopped() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.closed
}

// backoffDelay 指数退避 1s→30s（带抖动）。
func backoffDelay(attempt int) time.Duration {
	d := DefaultReconnectBase << attempt
	if d > DefaultReconnectMax {
		d = DefaultReconnectMax
	}
	d += time.Duration(randInt64(int64(time.Second)))
	return d
}

func randInt64(n int64) int64 {
	if n <= 0 {
		return 0
	}
	randMu.Lock()
	defer randMu.Unlock()
	return rand.Int63n(n)
}

func firstLanIP() string {
	addrs, err := net.InterfaceAddrs()
	if err != nil {
		return ""
	}
	for _, a := range addrs {
		if ipn, ok := a.(*net.IPNet); ok && !ipn.IP.IsLoopback() && ipn.IP.To4() != nil {
			return ipn.IP.String()
		}
	}
	return ""
}
