package channel

import (
	"context"
	"strings"
	"testing"
)

func TestConfigTransportDefault(t *testing.T) {
	cfg := Config{ClientID: "id", ClientSecret: "sec"}
	cfg.fill()
	if cfg.Transport != TransportStream {
		t.Fatalf("default transport = %q, want %q", cfg.Transport, TransportStream)
	}
	if err := cfg.validateTransport(); err != nil {
		t.Fatalf("stream should validate, got %v", err)
	}
}

func TestConfigTransportHTTPAccepted(t *testing.T) {
	cfg := Config{ClientID: "id", ClientSecret: "sec", Transport: TransportHTTP}
	if err := cfg.validateTransport(); err != nil {
		t.Fatalf("webhook should validate, got %v", err)
	}

	// webhook 无常驻连接：Start 引导使用 HTTPCallbackHandler。
	ch := New(cfg)
	ch.OnMessage(func(ctx context.Context, msg *IncomingMessage, reply Reply) error { return nil })
	if err := ch.Start(context.Background()); err == nil || !strings.Contains(err.Error(), "HTTPCallbackHandler") {
		t.Fatalf("Start with http transport should redirect to HTTPCallbackHandler, got %v", err)
	}
}

func TestConfigTransportUnknown(t *testing.T) {
	cfg := Config{ClientID: "id", ClientSecret: "sec", Transport: "grpc"}
	if err := cfg.validateTransport(); err == nil || !strings.Contains(err.Error(), "unknown transport") {
		t.Fatalf("unknown transport should be rejected, got %v", err)
	}
}
