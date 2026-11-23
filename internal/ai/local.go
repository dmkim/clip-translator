package ai

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const (
	DefaultLocalTimeout = 120 * time.Second
	DefaultLocalBaseURL = "http://localhost:11434/v1"
)

var ErrMissingLocalConfig = errors.New("로컬 LLM 주소와 모델을 설정하세요")

type LocalClient struct {
	baseURL string
	model   string
	apiKey  string
	timeout time.Duration
	http    *http.Client
}

func NewLocal(baseURL, model, apiKey string, timeout time.Duration) *LocalClient {
	if timeout <= 0 {
		timeout = DefaultLocalTimeout
	}
	baseURL = strings.TrimSpace(baseURL)
	if baseURL == "" {
		baseURL = DefaultLocalBaseURL
	}
	return &LocalClient{
		baseURL: strings.TrimRight(baseURL, "/"),
		model:   strings.TrimSpace(model),
		apiKey:  strings.TrimSpace(apiKey),
		timeout: timeout,
		http:    &http.Client{Timeout: timeout},
	}
}

func (c *LocalClient) Timeout() time.Duration {
	return c.timeout
}

type chatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type chatRequest struct {
	Model       string        `json:"model"`
	Messages    []chatMessage `json:"messages"`
	Temperature float64       `json:"temperature"`
	Stream      bool          `json:"stream"`
}

type chatResponse struct {
	Choices []struct {
		Message struct {
			Content string `json:"content"`
		} `json:"message"`
		FinishReason string `json:"finish_reason"`
	} `json:"choices"`
	Error *struct {
		Message string `json:"message"`
		Type    string `json:"type"`
	} `json:"error"`
}

func (c *LocalClient) Generate(ctx context.Context, systemPrompt, input string) (string, error) {
	if c.model == "" {
		return "", ErrMissingLocalConfig
	}

	endpoint, err := localEndpoint(c.baseURL, "/chat/completions")
	if err != nil {
		return "", err
	}

	content := input
	if system := strings.TrimSpace(systemPrompt); system != "" {
		content = "[Instructions]\n" + system + "\n\n[Input]\n" + input + "\n\nOutput only the result."
	}
	messages := []chatMessage{{Role: "user", Content: content}}

	payload, err := json.Marshal(chatRequest{
		Model:       c.model,
		Messages:    messages,
		Temperature: 0.3,
	})
	if err != nil {
		return "", fmt.Errorf("요청 생성 실패: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(payload))
	if err != nil {
		return "", fmt.Errorf("요청 생성 실패: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if c.apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+c.apiKey)
	}

	resp, err := c.http.Do(req)
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) || errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return "", fmt.Errorf("로컬 LLM 응답 시간이 초과되었습니다 (%s)", c.timeout)
		}
		return "", fmt.Errorf("로컬 LLM 연결 실패: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return "", fmt.Errorf("응답 읽기 실패: %w", err)
	}

	var parsed chatResponse
	if err := json.Unmarshal(body, &parsed); err != nil {
		return "", fmt.Errorf("응답 파싱 실패 (HTTP %d): %w", resp.StatusCode, err)
	}

	if parsed.Error != nil {
		message := parsed.Error.Message
		if message == "" {
			message = parsed.Error.Type
		}
		return "", fmt.Errorf("로컬 LLM 오류(HTTP %d): %s", resp.StatusCode, message)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("로컬 LLM 오류(HTTP %d): %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}

	for _, choice := range parsed.Choices {
		if out := strings.TrimSpace(choice.Message.Content); out != "" {
			return out, nil
		}
	}

	return "", errors.New("로컬 LLM이 빈 응답을 반환했습니다")
}

func PingLocal(ctx context.Context, baseURL, apiKey string) error {
	endpoint, err := localEndpoint(baseURL, "/models")
	if err != nil {
		return err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return fmt.Errorf("요청 생성 실패: %w", err)
	}
	if key := strings.TrimSpace(apiKey); key != "" {
		req.Header.Set("Authorization", "Bearer "+key)
	}

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) || errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return errors.New("로컬 LLM 연결 시간이 초과되었습니다")
		}
		return fmt.Errorf("로컬 LLM 연결 실패: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return fmt.Errorf("응답 읽기 실패: %w", err)
	}
	if resp.StatusCode == http.StatusNotFound || resp.StatusCode == http.StatusMethodNotAllowed {
		return fmt.Errorf("서버는 응답했지만 모델 목록 API를 지원하지 않습니다 (HTTP %d)", resp.StatusCode)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("로컬 LLM 오류(HTTP %d): %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	return nil
}

func ListLocalModels(ctx context.Context, baseURL, apiKey string) ([]string, error) {
	endpoint, err := localEndpoint(baseURL, "/models")
	if err != nil {
		return nil, err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, fmt.Errorf("요청 생성 실패: %w", err)
	}
	if key := strings.TrimSpace(apiKey); key != "" {
		req.Header.Set("Authorization", "Bearer "+key)
	}

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) || errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return nil, errors.New("로컬 LLM 연결 시간이 초과되었습니다")
		}
		return nil, fmt.Errorf("로컬 LLM 연결 실패: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, fmt.Errorf("응답 읽기 실패: %w", err)
	}
	if resp.StatusCode == http.StatusNotFound || resp.StatusCode == http.StatusMethodNotAllowed {
		return nil, fmt.Errorf("서버가 모델 목록 API를 지원하지 않습니다 (HTTP %d)", resp.StatusCode)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("로컬 LLM 오류(HTTP %d): %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}

	var parsed struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		return nil, fmt.Errorf("모델 목록 파싱 실패: %w", err)
	}

	models := make([]string, 0, len(parsed.Data))
	for _, item := range parsed.Data {
		if id := strings.TrimSpace(item.ID); id != "" {
			models = append(models, id)
		}
	}
	return models, nil
}

func localEndpoint(baseURL, suffix string) (string, error) {
	baseURL = strings.TrimSpace(baseURL)
	if baseURL == "" {
		baseURL = DefaultLocalBaseURL
	}

	u, err := url.Parse(baseURL)
	if err != nil {
		return "", fmt.Errorf("로컬 LLM 주소가 올바르지 않습니다: %w", err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return "", errors.New("로컬 LLM 주소는 http:// 또는 https://로 시작해야 합니다")
	}
	if u.Host == "" {
		return "", errors.New("로컬 LLM 주소에 호스트가 없습니다")
	}

	u.Path = strings.TrimRight(u.Path, "/") + suffix
	u.RawQuery = ""
	u.Fragment = ""
	return u.String(), nil
}
