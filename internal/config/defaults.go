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

	ProviderGemini = "gemini"
	ProviderLocal  = "local"

	DefaultLocalBaseURL        = "http://localhost:11434/v1"
	DefaultLocalTimeoutSeconds = 120

	oldWoWPrompt = "북미 WoW 인게임 채팅 번역기. 한국어를 비영어권 유저가 쓰기 좋은 자연스럽고 매너 있는 캐주얼 영어로 번역하라.\r\n" +
		"- 과한 슬랭·밈 금지. 보편적 약어(ty, np, mb, brb, omw, g2g, inv, oom, afk, gg)만 문맥에 맞게 사용.\r\n" +
		"- 마침표·대문자 최소화. 부연 설명이나 따옴표 없이 채팅에 바로 쓸 영어 한 문장만 출력."

	newWoWPrompt = "북미 WoW 인게임 채팅 번역기. 입력된 한국어를 북미 유저가 실제로 쓰는 자연스러운 캐주얼 영어로 번역하라.\r\n" +
		"- 절대 대화로 응답하지 말고 번역문만 출력.\r\n" +
		"- \"감사합니다\", \"고마워\" → \"ty\"\r\n" +
		"- \"천만에요\", \"괜찮아\" → \"np\"\r\n" +
		"- \"곧 돌아올게\" → \"brb\"\r\n" +
		"- \"잠시만\" → \"sec\"\r\n" +
		"- \"미안\" → \"sry\"\r\n" +
		"- 과한 슬랭·밈 금지. 보편적 약어(ty, np, mb, brb, omw, g2g, inv, oom, afk, gg)만 문맥에 맞게 사용.\r\n" +
		"- 마침표·대문자 최소화. 부연 설명이나 따옴표 없이 채팅에 바로 쓸 영어 한 문장만 출력."
)

// default.json is the template written when config.json does not exist. It
// mirrors the bundled presets and intentionally has an empty api_key.
//
//go:embed default.json
var defaultConfigJSON []byte

type bundledTemplate struct {
	Provider            string    `json:"provider"`
	Model               string    `json:"model"`
	LocalBaseURL        string    `json:"local_base_url"`
	LocalModel          string    `json:"local_model"`
	LocalTimeoutSeconds int       `json:"local_timeout_seconds"`
	Hotkey              string    `json:"hotkey"`
	AlwaysOnTop         bool      `json:"always_on_top"`
	ActiveProfile       string    `json:"active_profile"`
	Profiles            []Profile `json:"profiles"`
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
			Name:   DefaultProfileName,
			Prompt: newWoWPrompt,
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
