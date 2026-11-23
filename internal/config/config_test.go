package config

import (
	"testing"
	"time"
)

func TestNewDefaultProviderSettings(t *testing.T) {
	cfg := NewDefault()

	if got := cfg.ProviderName(); got != ProviderGemini {
		t.Errorf("ProviderName() = %q, want %q", got, ProviderGemini)
	}
	if got := cfg.LocalURL(); got != DefaultLocalBaseURL {
		t.Errorf("LocalURL() = %q, want %q", got, DefaultLocalBaseURL)
	}
	if got := cfg.LocalTimeout(); got != DefaultLocalTimeoutSeconds*time.Second {
		t.Errorf("LocalTimeout() = %v, want %v", got, DefaultLocalTimeoutSeconds*time.Second)
	}
	if cfg.APIKeyValue() != "" {
		t.Errorf("APIKeyValue() = %q, want empty", cfg.APIKeyValue())
	}
	if cfg.LocalAPIKeyValue() != "" {
		t.Errorf("LocalAPIKeyValue() = %q, want empty", cfg.LocalAPIKeyValue())
	}
}

func TestNormalizeProviderSettings(t *testing.T) {
	cfg := &Config{
		APIKey:              " key ",
		Model:               "gemini-2.5-flash-lite",
		Provider:            "",
		LocalBaseURL:        "  http://localhost:1234/v1  ",
		LocalModel:          "  qwen2.5  ",
		LocalAPIKey:         " local ",
		LocalTimeoutSeconds: 0,
	}
	cfg.normalize()

	if got := cfg.ProviderName(); got != ProviderGemini {
		t.Errorf("ProviderName() = %q, want %q", got, ProviderGemini)
	}
	if got := cfg.ModelName(); got != DefaultModel {
		t.Errorf("ModelName() = %q, want %q", got, DefaultModel)
	}
	if got := cfg.LocalURL(); got != "http://localhost:1234/v1" {
		t.Errorf("LocalURL() = %q", got)
	}
	if got := cfg.LocalModelName(); got != "qwen2.5" {
		t.Errorf("LocalModelName() = %q, want qwen2.5", got)
	}
	if got := cfg.LocalAPIKeyValue(); got != "local" {
		t.Errorf("LocalAPIKeyValue() = %q, want local", got)
	}
	if got := cfg.LocalTimeout(); got != DefaultLocalTimeoutSeconds*time.Second {
		t.Errorf("LocalTimeout() = %v", got)
	}
}

func TestLocalModelIsNotMigrated(t *testing.T) {
	cfg := &Config{
		Provider:   ProviderLocal,
		LocalModel: "gemini-2.5-flash-lite",
	}
	cfg.normalize()

	if got := cfg.ProviderName(); got != ProviderLocal {
		t.Errorf("ProviderName() = %q, want %q", got, ProviderLocal)
	}
	if got := cfg.LocalModelName(); got != "gemini-2.5-flash-lite" {
		t.Errorf("LocalModelName() = %q, want unchanged model", got)
	}
}

func TestUnknownProviderFallsBackToGemini(t *testing.T) {
	cfg := &Config{Provider: "something-else"}
	cfg.normalize()

	if got := cfg.ProviderName(); got != ProviderGemini {
		t.Errorf("ProviderName() = %q, want %q", got, ProviderGemini)
	}
}

func TestSetAIConfig(t *testing.T) {
	cfg := NewDefault()
	cfg.SetAIConfig(" LOCAL ", " http://localhost:8000/v1 ", " llama3.1 ", " token ", 45)

	if got := cfg.ProviderName(); got != ProviderLocal {
		t.Errorf("ProviderName() = %q, want %q", got, ProviderLocal)
	}
	if got := cfg.LocalURL(); got != "http://localhost:8000/v1" {
		t.Errorf("LocalURL() = %q", got)
	}
	if got := cfg.LocalModelName(); got != "llama3.1" {
		t.Errorf("LocalModelName() = %q", got)
	}
	if got := cfg.LocalAPIKeyValue(); got != "token" {
		t.Errorf("LocalAPIKeyValue() = %q", got)
	}
	if got := cfg.LocalTimeout(); got != 45*time.Second {
		t.Errorf("LocalTimeout() = %v, want 45s", got)
	}
}

func TestSetAIConfigRejectsInvalidTimeout(t *testing.T) {
	cfg := NewDefault()
	cfg.SetAIConfig(ProviderLocal, "", "", "", 0)

	if got := cfg.LocalTimeout(); got != DefaultLocalTimeoutSeconds*time.Second {
		t.Errorf("LocalTimeout() = %v, want default", got)
	}
}
