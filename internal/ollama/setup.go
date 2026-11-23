package ollama

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/genius/clip-translator/internal/ai"
)

const (
	BaseURL        = "http://127.0.0.1:11434/v1"
	TimeoutSeconds = 120

	defaultHost    = "127.0.0.1:11434"
	defaultAPIBase = "http://127.0.0.1:11434"
	warmupTimeout  = 5 * time.Minute
)

type Progress struct {
	Message string
	Percent int
}

type Result struct {
	BaseURL        string
	Model          string
	TimeoutSeconds int
}

func Setup(ctx context.Context, model string, progress func(Progress)) (Result, error) {
	model = strings.TrimSpace(model)
	if model == "" {
		return Result{}, errors.New("모델을 선택하세요")
	}

	report := func(message string, percent int) {
		if progress != nil {
			progress(Progress{Message: message, Percent: percent})
		}
	}

	exe, err := findOllama()
	if err != nil {
		report("winget 확인 중...", -1)
		winget, wingetErr := findWinget()
		if wingetErr != nil {
			return Result{}, wingetErr
		}

		report("winget으로 Ollama 설치 중...", -1)
		if err := installOllama(ctx, winget); err != nil {
			return Result{}, err
		}

		exe, err = findOllamaWithRetry(ctx)
		if err != nil {
			return Result{}, err
		}
	}

	serverEnv := []string{"OLLAMA_HOST=" + defaultHost}
	report("Ollama 서버 확인 중...", -1)
	if !serverReady(ctx, defaultAPIBase) {
		report("Ollama 서버 시작 중...", -1)
	}
	if err := ensureServer(ctx, exe, defaultHost, defaultAPIBase, serverEnv); err != nil {
		if ctx.Err() != nil {
			return Result{}, ctx.Err()
		}
		return Result{}, err
	}

	report(model+" 모델 확인 중...", -1)
	installed, err := hasModel(ctx, BaseURL, model)
	if err != nil {
		return Result{}, err
	}
	if !installed {
		report(model+" 모델 다운로드 중...", -1)
		if err := pullModel(ctx, exe, model, serverEnv, report); err != nil {
			return Result{}, err
		}
	}

	report("모델 워밍업 중... (최초 로드에는 수 분이 걸릴 수 있습니다)", -1)
	if err := warmupModel(ctx, BaseURL, model); err != nil {
		if ctx.Err() != nil {
			return Result{}, ctx.Err()
		}
		return Result{}, fmt.Errorf("모델 워밍업에 실패했습니다: %w", err)
	}

	pinCtx, cancelPin := context.WithTimeout(ctx, 30*time.Second)
	pinErr := PinModel(pinCtx, BaseURL, model)
	cancelPin()
	if ctx.Err() != nil {
		return Result{}, ctx.Err()
	}
	if pinErr != nil {
		return Result{}, fmt.Errorf("모델을 메모리에 유지하지 못했습니다: %w", pinErr)
	}

	report("완료", 100)
	return Result{
		BaseURL:        BaseURL,
		Model:          model,
		TimeoutSeconds: TimeoutSeconds,
	}, nil
}

func PinModel(ctx context.Context, baseURL, model string) error {
	model = strings.TrimSpace(model)
	if model == "" {
		return nil
	}
	if err := setKeepAlive(ctx, baseURL, model, -1); err != nil {
		return err
	}
	pinnedMu.Lock()
	pinnedBaseURL = baseURL
	pinnedModel = model
	pinnedMu.Unlock()
	return nil
}

func UnloadModel(ctx context.Context, baseURL, model string) error {
	model = strings.TrimSpace(model)
	if model == "" {
		return nil
	}
	loaded, err := modelLoaded(ctx, baseURL, model)
	if err != nil || !loaded {
		return err
	}
	return setKeepAlive(ctx, baseURL, model, 0)
}

func Shutdown(ctx context.Context) {
	stopOwnedServers()

	pinnedMu.Lock()
	baseURL, model := pinnedBaseURL, pinnedModel
	pinnedBaseURL, pinnedModel = "", ""
	pinnedMu.Unlock()
	if model == "" {
		return
	}
	_ = UnloadModel(ctx, baseURL, model)
}

func setKeepAlive(ctx context.Context, baseURL, model string, keepAlive int) error {
	model = strings.TrimSpace(model)
	if model == "" {
		return nil
	}

	endpoint, err := nativeEndpoint(baseURL, "/api/generate")
	if err != nil {
		return err
	}
	payload, err := json.Marshal(map[string]any{
		"model":      model,
		"prompt":     "",
		"stream":     false,
		"keep_alive": keepAlive,
	})
	if err != nil {
		return fmt.Errorf("요청 생성 실패: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(string(payload)))
	if err != nil {
		return fmt.Errorf("요청 생성 실패: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("Ollama 연결 실패: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return fmt.Errorf("응답 읽기 실패: %w", err)
	}
	if resp.StatusCode == http.StatusNotFound || resp.StatusCode == http.StatusMethodNotAllowed {
		return errors.New("Ollama API를 지원하지 않습니다")
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("Ollama 오류(HTTP %d): %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	return nil
}

func modelLoaded(ctx context.Context, baseURL, model string) (bool, error) {
	endpoint, err := nativeEndpoint(baseURL, "/api/ps")
	if err != nil {
		return false, err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return false, fmt.Errorf("요청 생성 실패: %w", err)
	}

	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return false, fmt.Errorf("Ollama 연결 실패: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return false, fmt.Errorf("응답 읽기 실패: %w", err)
	}
	if resp.StatusCode == http.StatusNotFound || resp.StatusCode == http.StatusMethodNotAllowed {
		return false, nil
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return false, fmt.Errorf("Ollama 오류(HTTP %d): %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}

	var parsed struct {
		Models []struct {
			Name string `json:"name"`
		} `json:"models"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		return false, fmt.Errorf("모델 상태 파싱 실패: %w", err)
	}
	for _, item := range parsed.Models {
		if item.Name == model {
			return true, nil
		}
	}
	return false, nil
}

func nativeEndpoint(baseURL, suffix string) (string, error) {
	u, err := url.Parse(strings.TrimSpace(baseURL))
	if err != nil {
		return "", fmt.Errorf("로컬 LLM 주소가 올바르지 않습니다: %w", err)
	}
	if (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return "", errors.New("로컬 LLM 주소는 http:// 또는 https://로 시작해야 합니다")
	}
	path := strings.TrimRight(u.Path, "/")
	path = strings.TrimSuffix(path, "/v1")
	u.Path = path + suffix
	u.RawQuery = ""
	u.Fragment = ""
	return u.String(), nil
}

func findOllama() (string, error) {
	if path, err := exec.LookPath("ollama.exe"); err == nil {
		return path, nil
	}

	var candidates []string
	if base := strings.TrimSpace(os.Getenv("LOCALAPPDATA")); base != "" {
		candidates = append(candidates, filepath.Join(base, "Programs", "Ollama", "ollama.exe"))
	}
	if base := strings.TrimSpace(os.Getenv("ProgramFiles")); base != "" {
		candidates = append(candidates, filepath.Join(base, "Ollama", "ollama.exe"))
	}

	for _, path := range candidates {
		if info, err := os.Stat(path); err == nil && !info.IsDir() {
			return path, nil
		}
	}
	return "", errors.New("Ollama가 설치되어 있지 않습니다")
}

func findOllamaWithRetry(ctx context.Context) (string, error) {
	deadline := time.Now().Add(10 * time.Second)
	for {
		if path, err := findOllama(); err == nil {
			return path, nil
		}
		if time.Now().After(deadline) {
			break
		}
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-time.After(300 * time.Millisecond):
		}
	}
	return "", errors.New("Ollama 설치 후 실행 파일을 찾지 못했습니다. 앱을 다시 시작한 뒤 다시 시도하세요")
}

func findWinget() (string, error) {
	if path, err := exec.LookPath("winget.exe"); err == nil {
		return path, nil
	}
	if base := strings.TrimSpace(os.Getenv("LOCALAPPDATA")); base != "" {
		path := filepath.Join(base, "Microsoft", "WindowsApps", "winget.exe")
		if info, err := os.Stat(path); err == nil && !info.IsDir() {
			return path, nil
		}
	}
	return "", errors.New("winget을 사용할 수 없습니다. Microsoft Store에서 App Installer를 설치하거나 Ollama를 직접 설치하세요")
}

func installOllama(ctx context.Context, winget string) error {
	out := newTailWriter(8 << 10)
	err := runProcess(ctx, winget, []string{
		"install",
		"--id", "Ollama.Ollama",
		"-e",
		"--silent",
		"--accept-package-agreements",
		"--accept-source-agreements",
		"--disable-interactivity",
	}, nil, out)
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if err == nil {
		return nil
	}
	if _, findErr := findOllama(); findErr == nil {
		return nil
	}

	message := fmt.Sprintf("winget으로 Ollama를 설치하지 못했습니다: %v", err)
	if tail := cleanOutput(out.String()); tail != "" {
		message += "\n" + tail
	}
	return errors.New(message)
}

var (
	serverMu        sync.Mutex
	serverProcesses = make(map[string]*os.Process)
	ensureMu        sync.Mutex

	pinnedMu      sync.Mutex
	pinnedBaseURL string
	pinnedModel   string
)

func ensureServer(ctx context.Context, exe, host, apiURL string, env []string) error {
	ensureMu.Lock()
	defer ensureMu.Unlock()

	if serverReady(ctx, apiURL) {
		return nil
	}
	if err := startServer(exe, host, env); err != nil && !serverReady(ctx, apiURL) {
		return fmt.Errorf("Ollama 서버를 시작하지 못했습니다: %w", err)
	}
	return waitServer(ctx, apiURL)
}

func startServer(exe, host string, env []string) error {
	cmd := exec.Command(exe, "serve")
	hideWindow(cmd)
	cmd.Env = mergeEnv(os.Environ(), env)
	cmd.Stdout = io.Discard
	cmd.Stderr = io.Discard
	if err := cmd.Start(); err != nil {
		return err
	}

	serverMu.Lock()
	serverProcesses[host] = cmd.Process
	serverMu.Unlock()

	go func() {
		_ = cmd.Wait()
		serverMu.Lock()
		if serverProcesses[host] == cmd.Process {
			delete(serverProcesses, host)
		}
		serverMu.Unlock()
	}()
	return nil
}

func stopOwnedServers() {
	serverMu.Lock()
	processes := make([]*os.Process, 0, len(serverProcesses))
	for host, process := range serverProcesses {
		processes = append(processes, process)
		delete(serverProcesses, host)
	}
	serverMu.Unlock()

	for _, process := range processes {
		killProcessTree(process)
	}
}

func serverReady(ctx context.Context, apiURL string) bool {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, apiURL+"/api/tags", nil)
	if err != nil {
		return false
	}
	client := &http.Client{Timeout: 2 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1024))
	return resp.StatusCode == http.StatusOK
}

func waitServer(ctx context.Context, apiURL string) error {
	deadline := time.Now().Add(30 * time.Second)
	for {
		if serverReady(ctx, apiURL) {
			return nil
		}
		if time.Now().After(deadline) {
			return errors.New("Ollama 서버가 30초 안에 시작되지 않았습니다")
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(500 * time.Millisecond):
		}
	}
}

func hasModel(ctx context.Context, baseURL, model string) (bool, error) {
	models, err := ai.ListLocalModels(ctx, baseURL, "")
	if err != nil {
		return false, fmt.Errorf("모델 목록을 확인하지 못했습니다: %w", err)
	}
	for _, installed := range models {
		if installed == model {
			return true, nil
		}
	}
	return false, nil
}

func pullModel(ctx context.Context, exe, model string, extraEnv []string, report func(string, int)) error {
	out := newTailWriter(16 << 10)
	out.onPercent = func(percent int) {
		report(model+" 모델 다운로드 중...", percent)
	}

	err := runProcess(ctx, exe, []string{"pull", model}, extraEnv, out)
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if err == nil {
		return nil
	}

	message := fmt.Sprintf("%s 모델 다운로드에 실패했습니다: %v", model, err)
	if tail := cleanOutput(out.String()); tail != "" {
		message += "\n" + tail
	}
	return errors.New(message)
}

func warmupModel(ctx context.Context, baseURL, model string) error {
	client := ai.NewLocal(baseURL, model, "", warmupTimeout)
	warmupCtx, cancel := context.WithTimeout(ctx, warmupTimeout)
	defer cancel()
	_, err := client.Generate(warmupCtx, "You are a warmup probe.", "Reply with the single word OK.")
	return err
}

func runProcess(ctx context.Context, name string, args, extraEnv []string, out io.Writer) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
	}

	cmd := exec.Command(name, args...)
	hideWindow(cmd)
	if len(extraEnv) > 0 {
		cmd.Env = mergeEnv(os.Environ(), extraEnv)
	}
	if out != nil {
		cmd.Stdout = out
		cmd.Stderr = out
	}
	if err := cmd.Start(); err != nil {
		return err
	}

	done := make(chan error, 1)
	go func() {
		done <- cmd.Wait()
	}()

	select {
	case err := <-done:
		return err
	case <-ctx.Done():
		killProcessTree(cmd.Process)
		select {
		case <-done:
		case <-time.After(5 * time.Second):
		}
		return ctx.Err()
	}
}

func killProcessTree(process *os.Process) {
	if process == nil {
		return
	}
	cmd := exec.Command("taskkill", "/PID", strconv.Itoa(process.Pid), "/T", "/F")
	hideWindow(cmd)
	if err := cmd.Run(); err != nil {
		_ = process.Kill()
	}
}

func hideWindow(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{
		HideWindow:    true,
		CreationFlags: 0x08000000,
	}
}

func withEnv(env []string, key, value string) []string {
	return mergeEnv(env, []string{key + "=" + value})
}

func mergeEnv(env, overrides []string) []string {
	keys := make(map[string]bool, len(overrides))
	for _, item := range overrides {
		if index := strings.IndexByte(item, '='); index > 0 {
			keys[strings.ToUpper(item[:index])] = true
		}
	}

	result := make([]string, 0, len(env)+len(overrides))
	for _, item := range env {
		if index := strings.IndexByte(item, '='); index > 0 {
			if keys[strings.ToUpper(item[:index])] {
				continue
			}
		}
		result = append(result, item)
	}
	return append(result, overrides...)
}

func cleanOutput(text string) string {
	text = strings.ReplaceAll(text, "\r", "\n")
	text = strings.TrimSpace(text)
	if len(text) > 1200 {
		text = text[len(text)-1200:]
	}
	return text
}

type tailWriter struct {
	mu         sync.Mutex
	buf        []byte
	max        int
	maxPercent int
	onPercent  func(int)
}

func newTailWriter(max int) *tailWriter {
	return &tailWriter{max: max, maxPercent: -1}
}

func (w *tailWriter) Write(p []byte) (int, error) {
	percent := maxPercentInChunk(p)
	call := false

	w.mu.Lock()
	w.buf = append(w.buf, p...)
	if len(w.buf) > w.max {
		w.buf = w.buf[len(w.buf)-w.max:]
	}
	if percent > w.maxPercent {
		w.maxPercent = percent
		call = w.onPercent != nil
	}
	w.mu.Unlock()

	if call {
		w.onPercent(percent)
	}
	return len(p), nil
}

func (w *tailWriter) String() string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return string(w.buf)
}

func maxPercentInChunk(data []byte) int {
	max := -1
	for i := 0; i < len(data); i++ {
		if data[i] != '%' {
			continue
		}
		j := i - 1
		for j >= 0 && data[j] >= '0' && data[j] <= '9' {
			j--
		}
		if j == i-1 {
			continue
		}
		value, err := strconv.Atoi(string(data[j+1 : i]))
		if err == nil && value >= 0 && value <= 100 && value > max {
			max = value
		}
	}
	return max
}
