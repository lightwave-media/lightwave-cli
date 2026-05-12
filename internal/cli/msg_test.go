package cli

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestPostMsgSend_HappyPath(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/msg/send" {
			t.Errorf("path = %q, want /msg/send", r.URL.Path)
		}
		if r.Method != http.MethodPost {
			t.Errorf("method = %q, want POST", r.Method)
		}
		if ct := r.Header.Get("Content-Type"); ct != "application/json" {
			t.Errorf("Content-Type = %q", ct)
		}
		var got MsgSendRequest
		body, _ := io.ReadAll(r.Body)
		if err := json.Unmarshal(body, &got); err != nil {
			t.Fatalf("decode body: %v", err)
		}
		if got.Channel != "joel-tg" || got.Persona != "v_core" || got.Text != "hello" {
			t.Errorf("body = %+v", got)
		}
		_ = json.NewEncoder(w).Encode(MsgSendResponse{
			MessageID: "msg-123",
			Status:    "queued",
		})
	}))
	defer srv.Close()

	t.Setenv("LW_GATEWAY_URL", srv.URL)
	msgSendTimeout = 2 * time.Second

	resp, err := postMsgSend(context.Background(), MsgSendRequest{
		Channel: "joel-tg",
		Persona: "v_core",
		Text:    "hello",
	})
	if err != nil {
		t.Fatalf("postMsgSend: %v", err)
	}
	if resp.MessageID != "msg-123" || resp.Status != "queued" {
		t.Errorf("resp = %+v", resp)
	}
}

func TestPostMsgSend_GatewayError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnprocessableEntity)
		_, _ = w.Write([]byte(`{"error":"unknown channel"}`))
	}))
	defer srv.Close()

	t.Setenv("LW_GATEWAY_URL", srv.URL)
	msgSendTimeout = 2 * time.Second

	_, err := postMsgSend(context.Background(), MsgSendRequest{
		Channel: "ghost",
		Persona: "v_core",
		Text:    "x",
	})
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "HTTP 422") {
		t.Errorf("err = %v, expected 'HTTP 422'", err)
	}
}

func TestPostMsgSend_Timeout(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		time.Sleep(200 * time.Millisecond)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	t.Setenv("LW_GATEWAY_URL", srv.URL)
	msgSendTimeout = 50 * time.Millisecond

	_, err := postMsgSend(context.Background(), MsgSendRequest{
		Channel: "joel-tg",
		Persona: "v_core",
		Text:    "x",
	})
	if err == nil {
		t.Fatal("expected timeout error, got nil")
	}
	if !strings.Contains(err.Error(), "timed out") && !strings.Contains(err.Error(), "deadline") {
		t.Errorf("err = %v, expected timeout-flavoured message", err)
	}
}

func TestGatewayBaseURL_EnvOverride(t *testing.T) {
	t.Setenv("LW_GATEWAY_URL", "http://elsewhere:1234/")
	if got := gatewayBaseURL(); got != "http://elsewhere:1234" {
		t.Errorf("gatewayBaseURL = %q (should trim trailing /)", got)
	}
}

func TestGatewayBaseURL_Default(t *testing.T) {
	t.Setenv("LW_GATEWAY_URL", "")
	if got := gatewayBaseURL(); got != "http://localhost:9701" {
		t.Errorf("gatewayBaseURL = %q, want default", got)
	}
}
