package channel

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math/rand"
	"net/http"
	"regexp"
	"strings"
	"sync"
	"time"
)

// flowStatus（SPEC §5）。
const (
	flowProcessing = "1"
	flowInputing   = "2"
	flowFinished   = "3"
	flowFailed     = "5"
)

// randAlphabet/randMu/randSuffix 随机后缀工具（卡片 outTrackId/guid 用）。
const randAlphabet = "abcdefghijklmnopqrstuvwxyz0123456789"

var randMu sync.Mutex

func randSuffix(n int) string {
	r := rand.New(rand.NewSource(time.Now().UnixNano()))
	b := make([]byte, n)
	randMu.Lock()
	for i := range b {
		b[i] = randAlphabet[r.Intn(len(randAlphabet))]
	}
	randMu.Unlock()
	return string(b)
}

// apiError 钉钉 API 错误；IsQpsLimit 判定 403 + code 含 QpsLimit。
type apiError struct {
	Status int
	Code   string
	Msg    string
	Body   string
}

func (e *apiError) Error() string {
	return fmt.Sprintf("dingtalk api error: http=%d code=%s msg=%s", e.Status, e.Code, e.Msg)
}

func (e *apiError) isQpsLimit() bool {
	return e.Status == http.StatusForbidden && strings.Contains(e.Code, "QpsLimit")
}

// APIStatus/APIIsQpsLimit 供 types.ClassifyError 读取错误上下文（接口匹配）。
func (e *apiError) APIStatus() int      { return e.Status }
func (e *apiError) APIIsQpsLimit() bool { return e.isQpsLimit() }

// cardClient AI 卡片五步协议客户端（SPEC §5），全局令牌桶限流（SPEC §6）。
type cardClient struct {
	cfg    *Config
	tokens *tokenProvider
	bucket *tokenBucket
	httpc  *http.Client
}

type cardTarget struct {
	IsGroup        bool
	ConversationID string // 群聊 openConversationId
	UserID         string // 单聊 senderStaffId（回退 senderId）
	RobotCode      string
}

func newCardClient(cfg *Config, tokens *tokenProvider, bucket *tokenBucket, httpc *http.Client) *cardClient {
	return &cardClient{cfg: cfg, tokens: tokens, bucket: bucket, httpc: httpc}
}

func (c *cardClient) call(ctx context.Context, method, path string, body any, out any) error {
	_, err := c.callRaw(ctx, method, path, body, out)
	return err
}

// callChecked 在 HTTP 200 内做业务级校验：卡片 API 会在 200 里返回
// {"result":[{"success":false,...}]}（dws 生产实证），必须视为失败。
func (c *cardClient) callChecked(ctx context.Context, method, path string, body any) error {
	raw, err := c.callRaw(ctx, method, path, body, nil)
	if err != nil {
		return err
	}
	if bytes.Contains(raw, []byte(`"success":false`)) {
		return &apiError{Status: 200, Code: "BusinessFailure", Msg: string(raw)}
	}
	return nil
}

func (c *cardClient) callRaw(ctx context.Context, method, path string, body any, out any) ([]byte, error) {
	var payload []byte
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		payload = b
	}

	do := func() ([]byte, error) {
		token, err := c.tokens.Get(ctx)
		if err != nil {
			return nil, err
		}
		req, err := http.NewRequestWithContext(ctx, method, c.cfg.APIBase+path, bytes.NewReader(payload))
		if err != nil {
			return nil, err
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("x-acs-dingtalk-access-token", token)
		resp, err := c.httpc.Do(req)
		if err != nil {
			return nil, err
		}
		defer resp.Body.Close()
		raw, _ := io.ReadAll(resp.Body)
		if resp.StatusCode >= http.StatusBadRequest {
			var e struct {
				Code string `json:"code"`
				Msg  string `json:"message"`
			}
			_ = json.Unmarshal(raw, &e)
			return nil, &apiError{Status: resp.StatusCode, Code: e.Code, Msg: e.Msg, Body: string(raw)}
		}
		if out != nil && len(raw) > 0 {
			if err := json.Unmarshal(raw, out); err != nil {
				return nil, err
			}
		}
		return raw, nil
	}

	// 全局限流：每次调用前取令牌；QpsLimit 触发退避并重试一次。
	if _, err := c.bucket.waitFor(ctx.Done()); err != nil {
		return nil, err
	}
	raw, err := do()
	if err != nil {
		var ae *apiError
		if ok := asAPIError(err, &ae); ok && ae.isQpsLimit() {
			c.bucket.triggerBackoff()
			if _, werr := c.bucket.waitFor(ctx.Done()); werr != nil {
				return nil, werr
			}
			return do()
		}
		return nil, err
	}
	return raw, nil
}

func asAPIError(err error, target **apiError) bool {
	if ae, ok := err.(*apiError); ok {
		*target = ae
		return true
	}
	return false
}

// 帧间隔（dws 实证）：deliver→内容→终帧背靠背会与客户端拉卡竞争，
// 间歇性渲染"内容加载失败"（干掉 #407 卡片尝试的故障）。留出间隔让客户端先订阅。
const cardFrameGap = 500 * time.Millisecond

// 单帧内容上限（dws/hermes 同款 MAX_MESSAGE_LENGTH 截断，rune 安全）。
const cardMaxContent = 20000

// cardInstance 一张已投递的 AI 卡片。
type cardInstance struct {
	OutTrackID      string
	inputingStarted bool
}

func (c *cardClient) createAndDeliver(ctx context.Context, t cardTarget) (*cardInstance, error) {
	outTrackID := fmt.Sprintf("card_%d_%s", time.Now().UnixMilli(), randSuffix(8))

	createBody := map[string]any{
		"cardTemplateId":        c.cfg.CardTemplateID,
		"outTrackId":            outTrackID,
		"cardData":              map[string]any{"cardParamMap": map[string]any{"config": `{"autoLayout":true}`}},
		"callbackType":          "STREAM",
		"imGroupOpenSpaceModel": map[string]any{"supportForward": true},
		"imRobotOpenSpaceModel": map[string]any{"supportForward": true},
	}
	if err := c.call(ctx, http.MethodPost, "/v1.0/card/instances", createBody, nil); err != nil {
		return nil, err
	}

	base := map[string]any{"outTrackId": outTrackID, "userIdType": 1}
	var deliverBody map[string]any
	if t.IsGroup {
		deliverBody = map[string]any{
			"outTrackId":              base["outTrackId"],
			"userIdType":              1,
			"openSpaceId":             "dtv1.card//IM_GROUP." + t.ConversationID,
			"imGroupOpenDeliverModel": map[string]any{"robotCode": t.RobotCode},
		}
	} else {
		deliverBody = map[string]any{
			"outTrackId":  base["outTrackId"],
			"userIdType":  1,
			"openSpaceId": "dtv1.card//IM_ROBOT." + t.UserID,
			"imRobotOpenDeliverModel": map[string]any{
				"spaceType": "IM_ROBOT",
				"robotCode": t.RobotCode,
				"extension": map[string]any{"dynamicSummary": "true"},
			},
		}
	}
	if err := c.callChecked(ctx, http.MethodPost, "/v1.0/card/instances/deliver", deliverBody); err != nil {
		return nil, err
	}
	return &cardInstance{OutTrackID: outTrackID}, nil
}

// setStatus 更新 flowStatus/msgContent（INPUTING 首帧 / FINISHED 收口）。
func (c *cardClient) setStatus(ctx context.Context, card *cardInstance, status, content string) error {
	body := map[string]any{
		"outTrackId": card.OutTrackID,
		"cardData": map[string]any{"cardParamMap": map[string]any{
			"flowStatus":        status,
			"msgContent":        content,
			"staticMsgContent":  "",
			"sys_full_json_obj": `{"order":["msgContent"]}`,
			"config":            `{"autoLayout":true}`,
		}},
	}
	if status == flowFinished {
		body["cardUpdateOptions"] = map[string]any{"updateCardDataByKey": true}
	}
	return c.call(ctx, http.MethodPut, "/v1.0/card/instances", body, nil)
}

// stream 更新流式内容；finalize=true 时为终帧。
func (c *cardClient) stream(ctx context.Context, card *cardInstance, content string, finalize bool) error {
	norm := normalizeForCard(content)
	if !finalize {
		norm = trailingNewlinesRe.ReplaceAllString(norm, "")
	}
	body := map[string]any{
		"outTrackId": card.OutTrackID,
		"guid":       fmt.Sprintf("%d_%s", time.Now().UnixMilli(), randSuffix(6)),
		"key":        "msgContent",
		"content":    norm,
		"isFull":     true,
		"isFinalize": finalize,
		"isError":    false,
	}
	return c.call(ctx, http.MethodPut, "/v1.0/card/streaming", body, nil)
}

var trailingNewlinesRe = regexp.MustCompile(`\n+$`)

// cardStreamer 面向业务的流式句柄：append 节流，finish 收口，fail 置错（E1–E4）。
type cardStreamer struct {
	ctx      context.Context
	cancel   context.CancelFunc
	client   *cardClient
	card     *cardInstance
	target   cardTarget
	throttle time.Duration

	mu          sync.Mutex
	accumulated string
	lastUpdate  time.Time
	closed      bool
	hasPending  bool
	frameCount  int
	lastFrameAt time.Time
	watchdog    *time.Timer
	aborted     bool
	fallback    func(ctx context.Context, text string) error // 卡片失败时的降级通道
	// deliverRest 超长内容超出单帧上限后，剩余部分的 webhook 续发通道（由 replier 注入）
	deliverRest func(text string) error
}

// flush-controller 效果参数：
// 窗口内更新不丢弃（trailing flush），长间隔后首刷延迟攒批。
const (
	longGapThreshold = 2 * time.Second
	longGapBatchWait = 300 * time.Millisecond
)

// CardDelivered 卡片是否真实创建并投递成功（livecheck/诊断用；false 表示处于降级模式）。
func (s *cardStreamer) CardDelivered() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.card != nil
}

func (s *cardStreamer) Append(delta string) error {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return fmt.Errorf("streamer already closed")
	}
	s.accumulated += delta
	if s.card == nil {
		s.mu.Unlock()
		return nil // 卡片不可用（E4 降级模式）：仅累积，Finish 时走降级
	}
	now := time.Now()
	elapsed := now.Sub(s.lastUpdate)
	switch {
	case elapsed >= s.throttle && elapsed > longGapThreshold:
		// 长间隔（工具调用/思考）后：延迟攒批，首个可见更新包含有意义内容。
		s.schedulePendingLocked(longGapBatchWait)
	case elapsed >= s.throttle:
		s.lastUpdate = now
		content := s.accumulated
		s.mu.Unlock()
		return s.update(content, false)
	case !s.hasPending:
		// 窗口内不丢弃：安排 trailing flush，内容最终必达。
		s.schedulePendingLocked(s.throttle - elapsed)
	}
	s.mu.Unlock()
	return nil
}

func (s *cardStreamer) schedulePendingLocked(d time.Duration) {
	if s.hasPending {
		return
	}
	s.hasPending = true
	time.AfterFunc(d, func() {
		s.mu.Lock()
		s.hasPending = false
		if s.closed || s.card == nil {
			s.mu.Unlock()
			return
		}
		s.lastUpdate = time.Now()
		content := s.accumulated
		s.mu.Unlock()
		_ = s.update(content, false)
	})
}

func (s *cardStreamer) update(content string, finalize bool) error {
	if s.card == nil {
		return fmt.Errorf("card unavailable")
	}
	// 首个内容帧与投递之间、终帧与上一帧之间留出间隔（防"内容加载失败"竞态）。
	if s.frameCount == 0 || finalize {
		if elapsed := time.Since(s.lastFrameAt); elapsed < cardFrameGap {
			select {
			case <-time.After(cardFrameGap - elapsed):
			case <-s.ctx.Done():
				return s.ctx.Err()
			}
		}
	}
	if r := []rune(content); len(r) > cardMaxContent {
		content = string(r[:cardMaxContent]) // rune 安全截断
	}
	// 共享字段（inputingStarted/frameCount/lastFrameAt/watchdog）统一在锁内变更，
	// HTTP 调用留在锁外——update 可能被 Append/trailing-flush 无锁并发调用。
	s.mu.Lock()
	card := s.card
	if card == nil {
		s.mu.Unlock()
		return fmt.Errorf("card unavailable")
	}
	if !card.inputingStarted {
		needInputing := true
		s.mu.Unlock()
		if err := s.client.setStatus(s.ctx, card, flowInputing, normalizeForCard(content)); err != nil {
			return err
		}
		s.mu.Lock()
		if s.card == card && !card.inputingStarted {
			card.inputingStarted = needInputing
		}
	}
	s.frameCount++
	s.lastFrameAt = time.Now()
	s.resetWatchdogLocked()
	card = s.card
	s.mu.Unlock()
	if card == nil {
		return fmt.Errorf("card unavailable")
	}
	return s.client.stream(s.ctx, card, content, finalize)
}

// armWatchdog 启动孤儿卡看门狗：超时未收口则强制 finish（connector 同款防线，
// 上游挂死/dispatch 不返回时卡片不会永久转圈）。
func (s *cardStreamer) armWatchdog() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.resetWatchdogLocked()
}

func (s *cardStreamer) resetWatchdogLocked() {
	if s.client.cfg.CardWatchdog <= 0 || s.card == nil {
		return
	}
	if s.watchdog != nil {
		s.watchdog.Stop()
	}
	s.watchdog = time.AfterFunc(s.client.cfg.CardWatchdog, func() {
		s.mu.Lock()
		if s.closed || s.card == nil {
			s.mu.Unlock()
			return
		}
		content := s.accumulated
		s.closed = true // 密封：迟到回调不得再建帧
		card := s.card
		s.mu.Unlock()
		_ = s.updateDirect(content, card)
	})
}

// updateDirect 绕过 closed 检查的收口（仅看门狗/内部使用）。
func (s *cardStreamer) updateDirect(content string, card *cardInstance) error {
	if err := s.client.stream(s.ctx, card, content, true); err != nil {
		return err
	}
	return s.client.setStatus(s.ctx, card, flowFinished, normalizeForCard(content))
}

// Finish 收口：终帧 + FINISHED。text 非空时覆盖累积内容。
func (s *cardStreamer) Finish(text string) error {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return nil // 幂等
	}
	s.closed = true
	if s.watchdog != nil {
		s.watchdog.Stop()
	}
	if text != "" {
		s.accumulated = text
	}
	content := s.accumulated
	card := s.card
	s.mu.Unlock()

	if card == nil {
		return s.tryFallback(content)
	}
	if err := s.update(content, true); err != nil {
		_ = s.tryFallback(content) // E4：降级保证用户拿到回复
		return err
	}
	err := s.client.setStatus(s.ctx, card, flowFinished, normalizeForCard(content))
	if err != nil {
		_ = s.tryFallback(content)
	}
	// 超长内容：卡片单帧截断（cardMaxContent）后，剩余部分经 webhook 分片续发，
	// 避免尾部静默丢失。best-effort，失败不影响卡片收口结果。
	if rest := overflowRemainder(content); rest != "" && s.deliverRest != nil {
		_ = s.deliverRest(rest)
	}
	s.cancel()
	return err
}

// overflowRemainder 返回超出单帧上限的尾部内容（无超出返回空）。
func overflowRemainder(content string) string {
	if r := []rune(content); len(r) > cardMaxContent {
		return string(r[cardMaxContent:])
	}
	return ""
}

// Abort 显式中止：密封流，卡片置 FAILED；
// 与 Finish（正常收口）/Fail（错误文案）互斥且幂等。
func (s *cardStreamer) Abort() error {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return nil
	}
	s.closed = true
	s.aborted = true
	if s.watchdog != nil {
		s.watchdog.Stop()
	}
	card := s.card
	content := s.accumulated
	s.mu.Unlock()
	if card == nil {
		return nil
	}
	_ = s.client.stream(s.ctx, card, content, true)
	return s.client.setStatus(s.ctx, card, flowFailed, normalizeForCard(content))
}

// Fail 置卡片为 FAILED 并降级发错误文本。
func (s *cardStreamer) Fail(errText string) error {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return nil
	}
	s.closed = true
	card := s.card
	s.mu.Unlock()

	if card != nil {
		if !card.inputingStarted {
			_ = s.client.setStatus(s.ctx, card, flowInputing, "")
			card.inputingStarted = true
		}
		_ = s.client.stream(s.ctx, card, errText, true)
		_ = s.client.setStatus(s.ctx, card, flowFailed, normalizeForCard(errText))
	}
	fbErr := s.tryFallback(errText)
	s.cancel()
	return fbErr
}

func (s *cardStreamer) tryFallback(text string) error {
	if s.fallback == nil || strings.TrimSpace(text) == "" {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	return s.fallback(ctx, text)
}
