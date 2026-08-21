# SafetyPipeline 快速入门

## 5分钟上手 SafetyPipeline

### 基础使用

```go
package main

import (
    "context"
    "log"
    
    "github.com/typefield/dingtalk-channel-sdk-go/internal/safety"
    "github.com/typefield/dingtalk-channel-sdk-go/types"
)

func main() {
    // 1. 创建安全管线
    cfg := types.DefaultSafetyConfig()
    
    opts := safety.PipelineOptions{
        OnMessage: handleMessage,
        OnReject:  handleReject,
        BotRobotCode: "your_robot_code",
    }
    
    pipeline := safety.NewSafetyPipeline(cfg, opts)
    defer pipeline.Dispose(context.Background())
    
    // 2. 推送消息
    msg := &types.IncomingMessage{
        ConversationID:   "chat123",
        ConversationType: types.ConversationTypeDM,
        SenderID:         "user456",
        SenderStaffID:    "staff456",
        MsgID:            "msg789",
        MsgType:          "text",
        Text:             "Hello!",
        CreateAt:         time.Now().UnixMilli(),
    }
    
    pipeline.PushMessage(context.Background(), "proto_id_1", msg)
}

func handleMessage(ctx context.Context, msg *types.IncomingMessage, sources []*types.IncomingMessage) error {
    log.Printf("Processing: %s", msg.Text)
    // 业务处理逻辑
    return nil
}

func handleReject(ctx context.Context, event *types.RejectEvent) {
    log.Printf("Rejected: %s, reason: %s", event.MessageID, event.Reason)
}
```

---

## 高级配置

### 启用媒体批处理

```go
cfg := types.SafetyConfig{
    Dedup: types.DedupConfig{
        TTL:               12 * time.Hour,
        MaxEntries:        5000,
        EnableFingerprint: true,
    },
    MediaBatch: types.MediaBatchConfig{
        Enabled:  true,
        DelayMs:  800,  // 800ms 窗口
        MaxItems: 9,    // 最多合并9个媒体
    },
    StaleWindow:  30 * time.Minute,
    DropSelfSent: true,
}

pipeline := safety.NewSafetyPipeline(cfg, opts)
```

**效果**: 用户连续上传5张图片 → 合并为一个批次 → 减少API调用

---

### 配置访问策略

```go
trueVal := true
falseVal := false

cfg := types.SafetyConfig{
    Policy: types.PolicyConfig{
        // 管理员绕过所有限制
        Admins: []string{"admin_staff_id"},
        
        // 全局发送者控制
        AllowFrom: []string{"user1", "user2"}, // 仅允许这些用户
        DenyFrom:  []string{"blocked_user"},   // 黑名单
        
        // DM 策略
        DMMode: "open", // "open" | "disabled" | "allowlist" | "blocklist"
        
        // 群组策略
        GroupAllowlist: []string{"group1", "group2"},
        RequireMention: &trueVal, // 群聊需要 @机器人
        RespondToMentionAll: &falseVal, // 不响应 @all
        
        // 单群覆盖配置
        GroupOverrides: map[string]types.GroupOverride{
            "special_group": {
                RequireMention:      &falseVal, // 该群不要求 @
                RespondToMentionAll: &trueVal,  // 该群响应 @all
                AllowFrom: []string{"user3"},   // 该群仅允许 user3
            },
        },
    },
}
```

---

### 启用 Redis 持久化（TODO）

```go
import "github.com/go-redis/redis/v8"

// 创建 Redis 客户端
redisClient := redis.NewClient(&redis.Options{
    Addr: "localhost:6379",
})

// 适配器（需要实现 safety.RedisClient 接口）
type redisAdapter struct {
    client *redis.Client
}

func (r *redisAdapter) Exists(key string) bool {
    val, _ := r.client.Exists(context.Background(), key).Result()
    return val > 0
}

func (r *redisAdapter) SetEx(key string, value string, ttl time.Duration) error {
    return r.client.Set(context.Background(), key, value, ttl).Err()
}

func (r *redisAdapter) Close() error {
    return r.client.Close()
}

// 使用 Redis
cfg := types.DedupConfig{
    TTL:        12 * time.Hour,
    MaxEntries: 5000,
}
redis := &redisAdapter{client: redisClient}
cache := safety.NewSeenCache(cfg, redis, "dd:seen:")
```

---

## 三层推送接口

### 1. PushMessage - 完整管线（消息）

```go
// 流程：过期 → 去重 → 自回复 → 策略 → 锁 → 批处理
pipeline.PushMessage(ctx, protoID, msg)
```

**适用**：普通文本消息、媒体消息

---

### 2. PushAction - 简化管线（卡片回调）

```go
// 流程：去重 → 锁 → 串行
pipeline.PushAction(ctx, eventID, queueScope, func() error {
    // 处理卡片回调
    return handleCardAction(event)
})
```

**适用**：卡片按钮点击、表单提交

---

### 3. PushLight - 最简管线（轻量事件）

```go
// 流程：仅去重
pipeline.PushLight(ctx, eventID, func() error {
    // 处理 reaction
    return handleReaction(event)
})
```

**适用**：消息 reaction、点赞

---

## 可观测性

### 监听拒绝事件

```go
opts := safety.PipelineOptions{
    OnReject: func(ctx context.Context, event *types.RejectEvent) {
        switch event.Reason {
        case types.RejectReasonStale:
            log.Printf("过期消息: %s", event.MessageID)
        case types.RejectReasonDuplicate:
            log.Printf("重复消息: %s", event.MessageID)
        case types.RejectReasonSelfSent:
            log.Printf("自回复: %s", event.MessageID)
        case types.RejectReasonDMDisabled:
            log.Printf("DM禁用: 来自 %s", event.SenderID)
        case types.RejectReasonGroupNotAllowed:
            log.Printf("群组未授权: %s", event.ChatID)
        case types.RejectReasonSenderDenied:
            log.Printf("发送者被拒: %s", event.SenderID)
        case types.RejectReasonLockContention:
            log.Printf("锁竞争: %s", event.MessageID)
        }
        
        // 可选：上报到监控系统
        // metrics.RecordReject(event.Reason)
    },
}
```

---

## 动态更新配置

```go
// 更新策略
newPolicy := types.PolicyConfig{
    GroupAllowlist: []string{"group1", "group2", "group3"},
}
pipeline.UpdatePolicy(newPolicy)

// 更新 Bot 身份
pipeline.SetBotIdentity("new_robot_code")
```

---

## 性能建议

### 1. 合理设置去重 TTL

```go
// 短期应用（聊天机器人）
cfg.Dedup.TTL = 1 * time.Hour

// 长期应用（客服系统）
cfg.Dedup.TTL = 24 * time.Hour
```

### 2. 调整媒体批处理延迟

```go
// 快速响应（低延迟）
cfg.MediaBatch.DelayMs = 500

// 更多合并（高吞吐）
cfg.MediaBatch.DelayMs = 1500
```

### 3. 启用内容指纹（推荐）

```go
cfg.Dedup.EnableFingerprint = true
```

**优势**：防止攻击者修改 msgId 后重投递

---

## 测试

### 单元测试

```go
func TestYourHandler(t *testing.T) {
    cfg := types.DefaultSafetyConfig()
    
    var received *types.IncomingMessage
    opts := safety.PipelineOptions{
        OnMessage: func(ctx context.Context, msg *types.IncomingMessage, sources []*types.IncomingMessage) error {
            received = msg
            return nil
        },
    }
    
    pipeline := safety.NewSafetyPipeline(cfg, opts)
    defer pipeline.Dispose(context.Background())
    
    msg := &types.IncomingMessage{
        ConversationID: "test_chat",
        MsgID:          "test_msg",
        MsgType:        "text",
        Text:           "test",
        CreateAt:       time.Now().UnixMilli(),
    }
    
    pipeline.PushMessage(context.Background(), "proto1", msg)
    
    time.Sleep(50 * time.Millisecond) // 等待异步处理
    
    if received == nil {
        t.Error("expected message to be processed")
    }
}
```

---

## 常见问题

### Q: SafetyPipeline 与现有 Channel 的关系？

A: SafetyPipeline 是独立的安全管线，可以：
- **独立使用**（新项目推荐）
- **集成到 Channel**（未来版本）
- **与现有代码并存**（当前版本）

### Q: 如何从旧的 Deduper 迁移？

A: SeenCache 向后兼容：
```go
// 旧方式（仍支持）
dedup := safety.NewDeduper(5 * time.Minute)

// 新方式
cache := safety.NewSeenCache(types.DedupConfig{TTL: 5 * time.Minute}, nil, "")
```

### Q: 媒体批处理会影响消息顺序吗？

A: 不会。当文本消息介入时，会自动刷新待处理的媒体批次。

### Q: 性能开销如何？

A: 极小。LRU 缓存查询 O(1)，锁操作 O(1)，策略检查 O(n) 其中 n 是白名单/黑名单大小。

---

## 更多资源

- [完整文档](./SAFETY_PIPELINE_SUMMARY.md)
- [测试用例](./internal/safety/*_test.go)

---

**祝你使用愉快！** 🎉
