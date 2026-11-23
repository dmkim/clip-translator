package ai

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"
)

var (
	_ Translator = (*Client)(nil)
	_ Translator = (*LocalClient)(nil)
)

type capturedChatRequest struct {
	Model           string        `json:"model"`
	Messages        []chatMessage `json:"messages"`
	Temperature     float64       `json:"temperature"`
	ReasoningEffort string        `json:"reasoning_effort"`
}

func TestLocalClientGenerate(t *testing.T) {
	var captured capturedChatRequest
	auth := ""

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("method = %s, want POST", r.Method)
		}
		if r.URL.Path != "/v1/chat/completions" {
			t.Errorf("path = %s, want /v1/chat/completions", r.URL.Path)
		}
		auth = r.Header.Get("Authorization")
		if err := json.NewDecoder(r.Body).Decode(&captured); err != nil {
			t.Errorf("decode request: %v", err)
		}
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"  hello  "}}]}`))
	}))
	defer srv.Close()

	client := NewLocal(srv.URL+"/v1", "qwen2.5", "secret", 5*time.Second)
	got, err := client.Generate(context.Background(), "system prompt", "user input")
	if err != nil {
		t.Fatalf("Generate() error = %v", err)
	}
	if got != "hello" {
		t.Errorf("Generate() = %q, want %q", got, "hello")
	}
	if auth != "Bearer secret" {
		t.Errorf("Authorization = %q, want %q", auth, "Bearer secret")
	}
	if captured.Model != "qwen2.5" {
		t.Errorf("model = %q, want qwen2.5", captured.Model)
	}
	if captured.Temperature != 0.3 {
		t.Errorf("temperature = %v, want 0.3", captured.Temperature)
	}
	if captured.ReasoningEffort != "none" {
		t.Errorf("reasoning_effort = %q, want none", captured.ReasoningEffort)
	}
	if len(captured.Messages) != 2 {
		t.Fatalf("messages = %d, want 2", len(captured.Messages))
	}
	if captured.Messages[0].Role != "system" || captured.Messages[0].Content != "system prompt" {
		t.Errorf("system message = %+v", captured.Messages[0])
	}
	if captured.Messages[1].Role != "user" || captured.Messages[1].Content != "user input" {
		t.Errorf("user message = %+v", captured.Messages[1])
	}
	if client.Timeout() != 5*time.Second {
		t.Errorf("Timeout() = %v, want 5s", client.Timeout())
	}
}

func TestLocalClientGenerateOmitsEmptySystemPrompt(t *testing.T) {
	var captured capturedChatRequest

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&captured); err != nil {
			t.Errorf("decode request: %v", err)
		}
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"ok"}}]}`))
	}))
	defer srv.Close()

	client := NewLocal(srv.URL+"/v1", "qwen2.5", "", time.Second)
	if _, err := client.Generate(context.Background(), "   ", "input"); err != nil {
		t.Fatalf("Generate() error = %v", err)
	}
	if len(captured.Messages) != 1 || captured.Messages[0].Role != "user" {
		t.Fatalf("messages = %+v, want one user message", captured.Messages)
	}
}

func TestLocalClientGenerateErrors(t *testing.T) {
	t.Run("missing model", func(t *testing.T) {
		client := NewLocal(DefaultLocalBaseURL, "", "", time.Second)
		_, err := client.Generate(context.Background(), "", "input")
		if !errors.Is(err, ErrMissingLocalConfig) {
			t.Fatalf("error = %v, want ErrMissingLocalConfig", err)
		}
	})

	t.Run("http error", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte(`{"error":{"message":"boom"}}`))
		}))
		defer srv.Close()

		client := NewLocal(srv.URL+"/v1", "qwen2.5", "", time.Second)
		_, err := client.Generate(context.Background(), "", "input")
		if err == nil || !strings.Contains(err.Error(), "boom") {
			t.Fatalf("error = %v, want message containing boom", err)
		}
	})

	t.Run("empty response", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_, _ = w.Write([]byte(`{"choices":[]}`))
		}))
		defer srv.Close()

		client := NewLocal(srv.URL+"/v1", "qwen2.5", "", time.Second)
		_, err := client.Generate(context.Background(), "", "input")
		if err == nil {
			t.Fatal("expected an error for an empty response")
		}
	})

	t.Run("invalid base url", func(t *testing.T) {
		client := NewLocal("localhost:11434/v1", "qwen2.5", "", time.Second)
		_, err := client.Generate(context.Background(), "", "input")
		if err == nil || !strings.Contains(err.Error(), "http://") {
			t.Fatalf("error = %v, want scheme validation error", err)
		}
	})
}

func TestPingLocal(t *testing.T) {
	t.Run("success", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path != "/v1/models" {
				t.Errorf("path = %s, want /v1/models", r.URL.Path)
			}
			if r.Header.Get("Authorization") != "Bearer secret" {
				t.Errorf("missing bearer token")
			}
			_, _ = w.Write([]byte(`{"data":[]}`))
		}))
		defer srv.Close()

		if err := PingLocal(context.Background(), srv.URL+"/v1", "secret"); err != nil {
			t.Fatalf("PingLocal() error = %v", err)
		}
	})

	t.Run("unsupported models endpoint", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusNotFound)
		}))
		defer srv.Close()

		err := PingLocal(context.Background(), srv.URL+"/v1", "")
		if err == nil || !strings.Contains(err.Error(), "모델 목록") {
			t.Fatalf("error = %v, want unsupported models endpoint", err)
		}
	})

	t.Run("connection error", func(t *testing.T) {
		err := PingLocal(context.Background(), "http://127.0.0.1:1/v1", "")
		if err == nil || !strings.Contains(err.Error(), "연결 실패") {
			t.Fatalf("error = %v, want connection failure", err)
		}
	})
}

func TestListLocalModels(t *testing.T) {
	t.Run("success", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path != "/v1/models" {
				t.Errorf("path = %s, want /v1/models", r.URL.Path)
			}
			if r.Header.Get("Authorization") != "Bearer secret" {
				t.Errorf("missing bearer token")
			}
			_, _ = w.Write([]byte(`{"data":[{"id":"qwen2.5-7b-instruct"},{"id":"  llama3.1  "},{"id":""}]}`))
		}))
		defer srv.Close()

		models, err := ListLocalModels(context.Background(), srv.URL+"/v1", "secret")
		if err != nil {
			t.Fatalf("ListLocalModels() error = %v", err)
		}
		want := []string{"qwen2.5-7b-instruct", "llama3.1"}
		if !reflect.DeepEqual(models, want) {
			t.Errorf("models = %v, want %v", models, want)
		}
	})

	t.Run("empty list", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_, _ = w.Write([]byte(`{"data":[]}`))
		}))
		defer srv.Close()

		models, err := ListLocalModels(context.Background(), srv.URL+"/v1", "")
		if err != nil {
			t.Fatalf("ListLocalModels() error = %v", err)
		}
		if len(models) != 0 {
			t.Errorf("models = %v, want empty", models)
		}
	})

	t.Run("unsupported models endpoint", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusNotFound)
		}))
		defer srv.Close()

		_, err := ListLocalModels(context.Background(), srv.URL+"/v1", "")
		if err == nil || !strings.Contains(err.Error(), "모델 목록") {
			t.Fatalf("error = %v, want unsupported models endpoint", err)
		}
	})

	t.Run("connection error", func(t *testing.T) {
		_, err := ListLocalModels(context.Background(), "http://127.0.0.1:1/v1", "")
		if err == nil || !strings.Contains(err.Error(), "연결 실패") {
			t.Fatalf("error = %v, want connection failure", err)
		}
	})
}

func TestLocalEndpoint(t *testing.T) {
	tests := []struct {
		name    string
		base    string
		want    string
		wantErr bool
	}{
		{name: "default", base: "", want: "http://localhost:11434/v1/chat/completions"},
		{name: "trailing slash", base: "http://localhost:11434/v1/", want: "http://localhost:11434/v1/chat/completions"},
		{name: "missing scheme", base: "localhost:11434/v1", wantErr: true},
		{name: "missing host", base: "http://", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := localEndpoint(tt.base, "/chat/completions")
			if tt.wantErr {
				if err == nil {
					t.Fatalf("expected an error, got %q", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("localEndpoint() error = %v", err)
			}
			if got != tt.want {
				t.Errorf("localEndpoint() = %q, want %q", got, tt.want)
			}
		})
	}
}
