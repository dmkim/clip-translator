package config

import (
	_ "embed"
	"encoding/json"
)

const (
	// DefaultHotkey is the global shortcut written to a missing config file.
	DefaultHotkey = "Ctrl+Shift+T"
	// DefaultModel is the Gemini model written to a missing config file.
	DefaultModel = "gemini-flash-lite-latest"
	// DefaultProfileName is the profile selected on first run.
	DefaultProfileName = "WoW 북미 서버 채팅"
)

// default.json is the template written when config.json does not exist. It
// mirrors the bundled presets and intentionally has an empty api_key.
//
//go:embed default.json
var defaultConfigJSON []byte

type bundledTemplate struct {
	Model         string    `json:"model"`
	Hotkey        string    `json:"hotkey"`
	AlwaysOnTop   bool      `json:"always_on_top"`
	ActiveProfile string    `json:"active_profile"`
	Profiles      []Profile `json:"profiles"`
}

// bundledDefaults parses the embedded template. It never returns an API key.
func bundledDefaults() (bundledTemplate, bool) {
	var tpl bundledTemplate
	if err := json.Unmarshal(defaultConfigJSON, &tpl); err != nil {
		return bundledTemplate{}, false
	}
	if len(tpl.Profiles) == 0 {
		return bundledTemplate{}, false
	}
	return tpl, true
}

func defaultProfiles() []Profile {
	return []Profile{
		{
			Name: "WoW 북미 서버 채팅",
			Prompt: "You are an expert English localization assistant specialized in World of Warcraft (NA realms). " +
				"Translate the Korean input into natural, native-style English that NA WoW players actually use in-game. " +
				"Use common shorthand/slang (np, ty, mb, afk, brb, pull, cc, kick, int, dispell, oom, lol, rip, gg). " +
				"Keep it casual, punchy, lowercase preference, minimal punctuation. " +
				"Output ONLY the single best translated sentence ready to paste into chat.",
		},
		{
			Name: "비즈니스/슬랙 영작",
			Prompt: "Translate the user's Korean input into polite, natural, and concise professional English suitable for Slack or workplace emails. " +
				"Output ONLY the translated English text without explanations.",
		},
		{
			Name: "영어 문법/네이티브 교정",
			Prompt: "Proofread and rewrite the user's English text to sound natural and native while preserving the original meaning and tone. " +
				"Output ONLY the corrected English text.",
		},
		{
			Name:   "핵심 3줄 요약 (한국어)",
			Prompt: "Summarize the provided text into 3 concise bullet points in Korean. Focus only on key takeaways. Output ONLY the summary.",
		},
	}
}
