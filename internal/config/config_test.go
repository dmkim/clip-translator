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
	if got := cfg.LocalModelName(); got != DefaultLocalModel {
		t.Errorf("LocalModelName() = %q, want %q", got, DefaultLocalModel)
	}
	models := cfg.LocalModelsList()
	if len(models) != 1 || models[0] != DefaultLocalModel {
		t.Errorf("LocalModelsList() = %v, want [%s]", models, DefaultLocalModel)
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

func TestNewDefaultWoWPrompt(t *testing.T) {
	cfg := NewDefault()
	profile, ok := cfg.Profile(DefaultProfileName)
	if !ok {
		t.Fatalf("default profile %q not found", DefaultProfileName)
	}
	if profile.Prompt != oldWoWPrompt {
		t.Errorf("default WoW prompt was not restored")
	}
}

func TestMigrateGeneratedWoWPrompts(t *testing.T) {
	for name, prompt := range map[string]string{
		"v0.8.1":       previousWoWPrompt,
		"few-shot":     fewShotWoWPrompt,
		"intermediate": "북미 WoW 인게임 채팅 번역기.\n예시:\n입력: 감사합니다\n출력: ty",
	} {
		t.Run(name, func(t *testing.T) {
			cfg := &Config{
				Profiles:      []Profile{{Name: DefaultProfileName, Prompt: prompt}},
				ActiveProfile: DefaultProfileName,
			}
			cfg.normalize()

			if cfg.Profiles[0].Prompt != oldWoWPrompt {
				t.Errorf("generated WoW prompt was not reverted")
			}
		})
	}
}

func TestNormalizePreservesLegacyLocalModels(t *testing.T) {
	cfg := &Config{
		LocalModel:  "gemma3:1b",
		LocalModels: []string{"gemma3:4b", "gemma3:1b"},
	}
	cfg.normalize()

	if got := cfg.LocalModelName(); got != "gemma3:1b" {
		t.Errorf("LocalModelName() = %q, want gemma3:1b", got)
	}
	models := cfg.LocalModelsList()
	if len(models) != 2 || models[0] != "gemma3:4b" || models[1] != "gemma3:1b" {
		t.Errorf("LocalModelsList() = %v, want [gemma3:4b gemma3:1b]", models)
	}
}

func TestNormalizeKeepsCustomLocalModel(t *testing.T) {
	cfg := &Config{
		LocalModel:  "my-model",
		LocalModels: []string{DefaultLocalModel},
	}
	cfg.normalize()

	if got := cfg.LocalModelName(); got != "my-model" {
		t.Errorf("LocalModelName() = %q, want my-model", got)
	}
	models := cfg.LocalModelsList()
	if len(models) != 2 || models[1] != "my-model" {
		t.Errorf("LocalModelsList() = %v, want custom model appended", models)
	}
}

func TestPreserveCustomWoWPrompt(t *testing.T) {
	cfg := &Config{
		Profiles:      []Profile{{Name: DefaultProfileName, Prompt: "custom prompt"}},
		ActiveProfile: DefaultProfileName,
	}
	cfg.normalize()

	if cfg.Profiles[0].Prompt != "custom prompt" {
		t.Errorf("custom prompt was overwritten: %q", cfg.Profiles[0].Prompt)
	}
}
