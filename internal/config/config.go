// Package config loads, normalizes and persists the clip-translator settings
// stored next to the executable as config.json.
package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// Profile is a named system prompt used to transform clipboard text.
type Profile struct {
	Name   string `json:"name"`
	Prompt string `json:"prompt"`
}

// Config is the persisted application configuration. All exported accessors
// and mutators are safe for concurrent use.
type Config struct {
	mu   sync.RWMutex
	path string

	APIKey              string    `json:"api_key"`
	Model               string    `json:"model"`
	Provider            string    `json:"provider"`
	LocalBaseURL        string    `json:"local_base_url"`
	LocalModel          string    `json:"local_model"`
	LocalModels         []string  `json:"local_models"`
	LocalAPIKey         string    `json:"local_api_key"`
	LocalTimeoutSeconds int       `json:"local_timeout_seconds"`
	Hotkey              string    `json:"hotkey"`
	AlwaysOnTop         bool      `json:"always_on_top"`
	ActiveProfile       string    `json:"active_profile"`
	Profiles            []Profile `json:"profiles"`
}

// Load reads config.json, creating it with defaults when it does not exist.
// A corrupt file is backed up to config.json.bad and replaced by defaults; the
// returned config is still usable in that case along with the error.
func Load() (*Config, error) {
	path, err := configPath()
	if err != nil {
		return nil, err
	}

	cfg := &Config{path: path}

	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		cfg.applyDefaults()
		if err := cfg.Save(); err != nil {
			return cfg, fmt.Errorf("기본 설정 파일 생성 실패: %w", err)
		}
		return cfg, nil
	}
	if err != nil {
		cfg.applyDefaults()
		return cfg, fmt.Errorf("설정 파일 읽기 실패: %w", err)
	}

	if err := json.Unmarshal(data, cfg); err != nil {
		_ = os.WriteFile(path+".bad", data, 0o600)
		cfg.applyDefaults()
		_ = cfg.Save()
		return cfg, fmt.Errorf("config.json 형식 오류로 기본 설정을 복원했습니다: %w", err)
	}

	cfg.normalize()
	if err := cfg.Save(); err != nil {
		return cfg, fmt.Errorf("설정 저장 실패: %w", err)
	}
	return cfg, nil
}

// NewDefault returns an in-memory default configuration. It is not persisted.
func NewDefault() *Config {
	cfg := &Config{}
	cfg.applyDefaults()
	return cfg
}

// Path returns the absolute path of the loaded config file.
func (c *Config) Path() string {
	if c == nil {
		return ""
	}
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.path
}

// Save atomically writes the configuration to disk.
func (c *Config) Save() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.saveLocked()
}

func (c *Config) saveLocked() error {
	data, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')

	if c.path == "" {
		return errors.New("설정 파일 경로가 지정되지 않았습니다")
	}
	if dir := filepath.Dir(c.path); dir != "" {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
	}

	tmp := c.path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	if err := os.Rename(tmp, c.path); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}

// APIKeyValue returns the configured Gemini API key.
func (c *Config) APIKeyValue() string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return strings.TrimSpace(c.APIKey)
}

// ModelName returns the configured Gemini model, falling back to the default.
func (c *Config) ModelName() string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return normalizeModel(c.Model)
}

// legacyModels maps previously used model IDs to the current default so
// existing config files follow the configured default after an update.
var legacyModels = map[string]string{
	"gemini-2.5-flash-lite": DefaultModel,
	"gemini-3.5-flash-lite": DefaultModel,
}

func normalizeModel(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return DefaultModel
	}
	if replacement, ok := legacyModels[strings.ToLower(value)]; ok {
		return replacement
	}
	return value
}

func normalizeProvider(value string) string {
	if strings.EqualFold(strings.TrimSpace(value), ProviderLocal) {
		return ProviderLocal
	}
	return ProviderGemini
}

func (c *Config) ProviderName() string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return normalizeProvider(c.Provider)
}

func (c *Config) LocalURL() string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	if strings.TrimSpace(c.LocalBaseURL) == "" {
		return DefaultLocalBaseURL
	}
	return strings.TrimSpace(c.LocalBaseURL)
}

func (c *Config) LocalModelName() string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return strings.TrimSpace(c.LocalModel)
}

func (c *Config) LocalAPIKeyValue() string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return strings.TrimSpace(c.LocalAPIKey)
}

func (c *Config) LocalTimeout() time.Duration {
	c.mu.RLock()
	defer c.mu.RUnlock()
	seconds := c.LocalTimeoutSeconds
	if seconds <= 0 {
		seconds = DefaultLocalTimeoutSeconds
	}
	return time.Duration(seconds) * time.Second
}

func (c *Config) LocalModelsList() []string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return append([]string(nil), c.LocalModels...)
}

func (c *Config) SetAIConfig(provider, baseURL, model, apiKey string, timeoutSeconds int) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if timeoutSeconds <= 0 {
		timeoutSeconds = DefaultLocalTimeoutSeconds
	}
	c.Provider = normalizeProvider(provider)
	c.LocalBaseURL = strings.TrimSpace(baseURL)
	c.LocalModel = strings.TrimSpace(model)
	if c.LocalModel != "" && !containsString(c.LocalModels, c.LocalModel) {
		c.LocalModels = append(c.LocalModels, c.LocalModel)
	}
	c.LocalAPIKey = strings.TrimSpace(apiKey)
	c.LocalTimeoutSeconds = timeoutSeconds
}

// HotkeySpec returns the configured global hotkey, falling back to the default.
func (c *Config) HotkeySpec() string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	if strings.TrimSpace(c.Hotkey) == "" {
		return DefaultHotkey
	}
	return strings.TrimSpace(c.Hotkey)
}

// SetHotkey updates the global shortcut specification.
func (c *Config) SetHotkey(spec string) {
	c.mu.Lock()
	c.Hotkey = strings.TrimSpace(spec)
	c.mu.Unlock()
}

// IsAlwaysOnTop reports whether the window should stay above other windows.
func (c *Config) IsAlwaysOnTop() bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.AlwaysOnTop
}

// SetAlwaysOnTop updates the always-on-top flag.
func (c *Config) SetAlwaysOnTop(v bool) {
	c.mu.Lock()
	c.AlwaysOnTop = v
	c.mu.Unlock()
}

// SetAPIKey updates the Gemini API key.
func (c *Config) SetAPIKey(key string) {
	c.mu.Lock()
	c.APIKey = strings.TrimSpace(key)
	c.mu.Unlock()
}

// ProfileNames returns the ordered profile names.
func (c *Config) ProfileNames() []string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	names := make([]string, len(c.Profiles))
	for i, p := range c.Profiles {
		names[i] = p.Name
	}
	return names
}

// AllProfiles returns a copy of the configured profiles.
func (c *Config) AllProfiles() []Profile {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return append([]Profile(nil), c.Profiles...)
}

// ActiveProfileName returns the name of the currently selected profile.
func (c *Config) ActiveProfileName() string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.ActiveProfile
}

// Active returns the currently selected profile, or a safe fallback prompt.
func (c *Config) Active() Profile {
	c.mu.RLock()
	defer c.mu.RUnlock()
	for _, p := range c.Profiles {
		if p.Name == c.ActiveProfile {
			return p
		}
	}
	if len(c.Profiles) > 0 {
		return c.Profiles[0]
	}
	return Profile{Name: DefaultProfileName, Prompt: defaultProfiles()[0].Prompt}
}

// Profile returns a named profile.
func (c *Config) Profile(name string) (Profile, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	for _, p := range c.Profiles {
		if p.Name == name {
			return p, true
		}
	}
	return Profile{}, false
}

// SetActiveProfile selects an existing profile. It reports whether the name
// matched a known profile.
func (c *Config) SetActiveProfile(name string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, p := range c.Profiles {
		if p.Name == name {
			c.ActiveProfile = name
			return true
		}
	}
	return false
}

// SetPrompt replaces the prompt of the named profile.
func (c *Config) SetPrompt(name, prompt string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	for i := range c.Profiles {
		if c.Profiles[i].Name == name {
			c.Profiles[i].Prompt = prompt
			return true
		}
	}
	return false
}

// RenameProfile changes the name of an existing profile, keeping the active
// selection in sync. Empty names and names already used by another profile are
// rejected.
func (c *Config) RenameProfile(oldName, newName string) (Profile, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	newName = strings.TrimSpace(newName)
	if newName == "" {
		return Profile{}, errors.New("프리셋 이름을 입력하세요")
	}

	idx := -1
	for i := range c.Profiles {
		if c.Profiles[i].Name == oldName {
			idx = i
			break
		}
	}
	if idx < 0 {
		return Profile{}, fmt.Errorf("프리셋을 찾을 수 없습니다: %s", oldName)
	}
	if newName == oldName {
		return c.Profiles[idx], nil
	}
	if c.nameTakenLocked(newName) {
		return Profile{}, fmt.Errorf("이미 존재하는 이름입니다: %s", newName)
	}

	c.Profiles[idx].Name = newName
	if c.ActiveProfile == oldName {
		c.ActiveProfile = newName
	}
	return c.Profiles[idx], nil
}

// AddProfile appends a new profile and makes it active. A unique name is
// generated when base is empty or already taken.
func (c *Config) AddProfile(base, prompt string) Profile {
	c.mu.Lock()
	defer c.mu.Unlock()

	base = strings.TrimSpace(base)
	if base == "" {
		base = "새 프리셋"
	}
	name := base
	for i := 2; c.nameTakenLocked(name); i++ {
		name = fmt.Sprintf("%s %d", base, i)
	}

	p := Profile{Name: name, Prompt: prompt}
	c.Profiles = append(c.Profiles, p)
	c.ActiveProfile = name
	return p
}

// RemoveProfile deletes the named profile and returns the profile that became
// active. Removing the last remaining profile is refused.
func (c *Config) RemoveProfile(name string) (Profile, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if len(c.Profiles) <= 1 {
		return Profile{}, errors.New("프리셋은 최소 1개가 필요합니다")
	}

	idx := -1
	for i := range c.Profiles {
		if c.Profiles[i].Name == name {
			idx = i
			break
		}
	}
	if idx < 0 {
		return Profile{}, fmt.Errorf("프리셋을 찾을 수 없습니다: %s", name)
	}

	c.Profiles = append(c.Profiles[:idx], c.Profiles[idx+1:]...)
	for _, p := range c.Profiles {
		if p.Name == c.ActiveProfile {
			return p, nil
		}
	}
	c.ActiveProfile = c.Profiles[0].Name
	return c.Profiles[0], nil
}

func (c *Config) nameTakenLocked(name string) bool {
	for _, p := range c.Profiles {
		if p.Name == name {
			return true
		}
	}
	return false
}

func (c *Config) normalizeLocalModelsLocked() {
	models := make([]string, 0, len(c.LocalModels))
	seen := make(map[string]bool, len(c.LocalModels))
	for _, model := range c.LocalModels {
		model = strings.TrimSpace(model)
		if model == "" || seen[model] {
			continue
		}
		seen[model] = true
		models = append(models, model)
	}
	if len(models) == 0 {
		models = defaultLocalModels()
		for _, model := range models {
			seen[model] = true
		}
	}

	c.LocalModels = models
	c.LocalModel = strings.TrimSpace(c.LocalModel)
	if c.LocalModel == "" {
		c.LocalModel = c.LocalModels[0]
	}
	if !seen[c.LocalModel] {
		c.LocalModels = append(c.LocalModels, c.LocalModel)
	}
}

func containsString(list []string, value string) bool {
	for _, item := range list {
		if item == value {
			return true
		}
	}
	return false
}

func (c *Config) migrateWoWPromptLocked() {
	for i := range c.Profiles {
		if isModifiedWoWPrompt(c.Profiles[i].Prompt) {
			c.Profiles[i].Prompt = oldWoWPrompt
		}
	}
}

func isModifiedWoWPrompt(prompt string) bool {
	switch prompt {
	case previousWoWPrompt, fewShotWoWPrompt:
		return true
	}
	if !strings.Contains(prompt, "WoW") {
		return false
	}
	return strings.Contains(prompt, "절대 대화로 응답하지 말") ||
		strings.Contains(prompt, "입력: 감사합니다") ||
		strings.Contains(prompt, "곧 돌아올게") ||
		strings.Contains(prompt, "천만에요")
}

func (c *Config) applyDefaults() {
	if tpl, ok := bundledDefaults(); ok {
		c.Provider = tpl.Provider
		c.Model = tpl.Model
		c.LocalBaseURL = tpl.LocalBaseURL
		c.LocalModel = tpl.LocalModel
		c.LocalModels = tpl.LocalModels
		c.LocalTimeoutSeconds = tpl.LocalTimeoutSeconds
		c.Hotkey = tpl.Hotkey
		c.AlwaysOnTop = tpl.AlwaysOnTop
		c.ActiveProfile = tpl.ActiveProfile
		c.Profiles = tpl.Profiles
	} else {
		c.Provider = ProviderGemini
		c.AlwaysOnTop = false
		c.Hotkey = DefaultHotkey
		c.Model = DefaultModel
		c.LocalBaseURL = DefaultLocalBaseURL
		c.LocalModel = DefaultLocalModel
		c.LocalModels = defaultLocalModels()
		c.LocalTimeoutSeconds = DefaultLocalTimeoutSeconds
		c.ActiveProfile = DefaultProfileName
		c.Profiles = defaultProfiles()
	}

	// The generated config must never contain an API key.
	c.APIKey = ""
	c.LocalAPIKey = ""
	c.Model = normalizeModel(c.Model)
	if len(c.Profiles) == 0 {
		c.Profiles = defaultProfiles()
	}
	c.normalizeLocked()
}

func (c *Config) normalize() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.normalizeLocked()
}

func (c *Config) normalizeLocked() {
	if strings.TrimSpace(c.Hotkey) == "" {
		c.Hotkey = DefaultHotkey
	}
	c.Model = normalizeModel(c.Model)
	c.Provider = normalizeProvider(c.Provider)
	c.LocalBaseURL = strings.TrimSpace(c.LocalBaseURL)
	if c.LocalBaseURL == "" {
		c.LocalBaseURL = DefaultLocalBaseURL
	}
	c.normalizeLocalModelsLocked()
	c.LocalAPIKey = strings.TrimSpace(c.LocalAPIKey)
	if c.LocalTimeoutSeconds <= 0 {
		c.LocalTimeoutSeconds = DefaultLocalTimeoutSeconds
	}
	if len(c.Profiles) == 0 {
		c.Profiles = defaultProfiles()
	}
	for i := range c.Profiles {
		if strings.TrimSpace(c.Profiles[i].Name) == "" {
			c.Profiles[i].Name = fmt.Sprintf("프리셋 %d", i+1)
		}
	}
	c.migrateWoWPromptLocked()
	if c.ActiveProfile == "" {
		c.ActiveProfile = c.Profiles[0].Name
		return
	}
	for _, p := range c.Profiles {
		if p.Name == c.ActiveProfile {
			return
		}
	}
	c.ActiveProfile = c.Profiles[0].Name
}

// configPath prefers a config.json next to the executable. When the executable
// lives in the system temp directory (e.g. "go run"), the per-user config
// directory is used instead so development runs do not write into %TEMP%.
func configPath() (string, error) {
	if exe, err := os.Executable(); err == nil {
		dir := filepath.Dir(exe)
		if !isUnderTempDir(dir) {
			return filepath.Join(dir, "config.json"), nil
		}
	}

	base, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("설정 디렉터리를 찾을 수 없습니다: %w", err)
	}
	return filepath.Join(base, "clip-translator", "config.json"), nil
}

func isUnderTempDir(dir string) bool {
	tmp, err := filepath.Abs(os.TempDir())
	if err != nil {
		return false
	}
	abs, err := filepath.Abs(dir)
	if err != nil {
		return false
	}
	rel, err := filepath.Rel(tmp, abs)
	if err != nil {
		return false
	}
	return rel == "." || (rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)))
}
