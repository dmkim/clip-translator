// Package ai implements a minimal Google Gemini REST client.
package ai

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const (
	endpoint = "https://generativelanguage.googleapis.com/v1beta/models/%s:generateContent"
	// DefaultModel is used when the caller does not specify one.
	DefaultModel = "gemini-flash-lite-latest"
	// DefaultTimeout bounds a single generation request. Gemini 3 models can
	// take longer than the old 8 second budget, especially on cold starts.
	DefaultTimeout = 30 * time.Second
	// warmupURL is used to pre-establish DNS/TCP/TLS without an API call.
	warmupURL = "https://generativelanguage.googleapis.com/"
)

// sharedHTTPClient keeps warmed connections alive across translations.
var sharedHTTPClient = &http.Client{
	Timeout: DefaultTimeout,
	Transport: &http.Transport{
		Proxy: http.ProxyFromEnvironment,
		DialContext: (&net.Dialer{
			Timeout:   10 * time.Second,
			KeepAlive: 30 * time.Second,
		}).DialContext,
		ForceAttemptHTTP2:     true,
		MaxIdleConns:          16,
		MaxIdleConnsPerHost:   4,
		IdleConnTimeout:       30 * time.Minute,
		TLSHandshakeTimeout:   10 * time.Second,
		ExpectContinueTimeout: 1 * time.Second,
	},
}

// ErrMissingAPIKey is returned when the client is used without an API key.
var ErrMissingAPIKey = errors.New("Gemini API 키가 설정되지 않았습니다. 하단 API 설정에서 키를 저장하세요")

// Client calls the Gemini generateContent endpoint.
type Client struct {
	apiKey string
	model  string
	http   *http.Client
}

// New creates a client. An empty model selects DefaultModel.
func New(apiKey, model string) *Client {
	model = strings.TrimSpace(model)
	if model == "" {
		model = DefaultModel
	}
	return &Client{
		apiKey: strings.TrimSpace(apiKey),
		model:  model,
		http:   sharedHTTPClient,
	}
}

// Warmup pre-establishes DNS, TCP and TLS for the Gemini endpoint so the first
// translation can reuse an existing connection. It makes no API request and is
// safe to call in a background goroutine.
func Warmup() {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodHead, warmupURL, nil)
	if err != nil {
		return
	}
	resp, err := sharedHTTPClient.Do(req)
	if err != nil {
		return
	}
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1024))
	_ = resp.Body.Close()
}

// KeepAlive warms up immediately and then refreshes the idle Gemini connection
// periodically so a translation after a long pause does not pay the full
// DNS/TCP/TLS setup cost.
func KeepAlive(ctx context.Context, interval time.Duration) {
	if interval <= 0 {
		interval = 5 * time.Minute
	}

	Warmup()
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			Warmup()
		}
	}
}

type generateRequest struct {
	SystemInstruction *content         `json:"system_instruction,omitempty"`
	Contents          []content        `json:"contents"`
	GenerationConfig  generationConfig `json:"generationConfig"`
}

type content struct {
	Role  string `json:"role,omitempty"`
	Parts []part `json:"parts"`
}

type part struct {
	Text string `json:"text"`
}

type generationConfig struct {
	Temperature    float64         `json:"temperature"`
	ThinkingConfig *thinkingConfig `json:"thinkingConfig,omitempty"`
}

type thinkingConfig struct {
	ThinkingBudget *int   `json:"thinkingBudget,omitempty"`
	ThinkingLevel  string `json:"thinkingLevel,omitempty"`
}

// generationConfigFor disables or minimizes internal reasoning where the model
// supports it. Gemini 3 Flash-Lite rejects thinkingBudget and uses
// thinkingLevel instead, while Gemini 2.5 Flash accepts a zero budget.
func generationConfigFor(model string) generationConfig {
	cfg := generationConfig{Temperature: 0.3}
	name := strings.ToLower(model)

	switch {
	case strings.Contains(name, "gemini-2.5-flash"):
		budget := 0
		cfg.ThinkingConfig = &thinkingConfig{ThinkingBudget: &budget}
	case strings.Contains(name, "flash-lite"):
		cfg.ThinkingConfig = &thinkingConfig{ThinkingLevel: "minimal"}
	}
	return cfg
}

type generateResponse struct {
	Candidates []struct {
		Content struct {
			Parts []struct {
				Text string `json:"text"`
			} `json:"parts"`
		} `json:"content"`
		FinishReason string `json:"finishReason"`
	} `json:"candidates"`
	PromptFeedback *struct {
		BlockReason string `json:"blockReason"`
	} `json:"promptFeedback"`
	Error *struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
		Status  string `json:"status"`
	} `json:"error"`
}

func (c *Client) Timeout() time.Duration {
	return c.http.Timeout
}

// Generate sends systemPrompt and input to Gemini and returns the response
// text with surrounding whitespace removed.
func (c *Client) Generate(ctx context.Context, systemPrompt, input string) (string, error) {
	if c.apiKey == "" {
		return "", ErrMissingAPIKey
	}

	reqBody := generateRequest{
		Contents: []content{{
			Role:  "user",
			Parts: []part{{Text: input}},
		}},
		GenerationConfig: generationConfigFor(c.model),
	}
	if strings.TrimSpace(systemPrompt) != "" {
		reqBody.SystemInstruction = &content{
			Parts: []part{{Text: systemPrompt}},
		}
	}

	payload, err := json.Marshal(reqBody)
	if err != nil {
		return "", fmt.Errorf("요청 생성 실패: %w", err)
	}

	u := fmt.Sprintf(endpoint, url.PathEscape(c.model))
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u, bytes.NewReader(payload))
	if err != nil {
		return "", fmt.Errorf("요청 생성 실패: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-goog-api-key", c.apiKey)

	resp, err := c.http.Do(req)
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) || errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return "", fmt.Errorf("Gemini 응답 시간이 초과되었습니다 (%s)", c.http.Timeout)
		}
		return "", fmt.Errorf("네트워크 오류: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return "", fmt.Errorf("응답 읽기 실패: %w", err)
	}

	var parsed generateResponse
	if err := json.Unmarshal(body, &parsed); err != nil {
		return "", fmt.Errorf("응답 파싱 실패 (HTTP %d): %w", resp.StatusCode, err)
	}

	if parsed.Error != nil {
		return "", fmt.Errorf("Gemini 오류(HTTP %d): %s", resp.StatusCode, parsed.Error.Message)
	}
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("Gemini 오류(HTTP %d): %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}

	var sb strings.Builder
	for _, cand := range parsed.Candidates {
		for _, p := range cand.Content.Parts {
			sb.WriteString(p.Text)
		}
	}

	out := strings.TrimSpace(sb.String())
	if out == "" {
		if parsed.PromptFeedback != nil && parsed.PromptFeedback.BlockReason != "" {
			return "", fmt.Errorf("요청이 차단되었습니다: %s", parsed.PromptFeedback.BlockReason)
		}
		return "", errors.New("Gemini가 빈 응답을 반환했습니다")
	}
	return out, nil
}
