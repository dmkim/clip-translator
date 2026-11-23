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
	Model4B        = "gemma3:4b"
	Model1B        = "gemma3:1b"
	TimeoutSeconds = 120

	apiBase          = "http://127.0.0.1:11434"
	minVRAMFor4B     = 6 * 1024
	nvidiaSMITimeout = 3 * time.Second
	warmupTimeout    = 5 * time.Minute
)

type Hardware struct {
	GPU      string
	VRAMMB   int
	Detected bool
}

type ModelChoice struct {
	Model    string
	Hardware Hardware
	Reason   string
}

func (c ModelChoice) Description() string {
	if !c.Hardware.Detected {
		return "GPU 자동 감지 불가 → 안정성을 위해 " + c.Model + " 선택"
	}
	return fmt.Sprintf("%s · VRAM %dMB → %s 선택", c.Hardware.GPU, c.Hardware.VRAMMB, c.Model)
}

func DetectModel() ModelChoice {
	hardware := DetectHardware()
	model := selectModel(hardware)
	reason := "GPU 자동 감지 불가"
	if hardware.Detected {
		if hardware.VRAMMB >= minVRAMFor4B {
			reason = "VRAM 6GB 이상"
		} else {
			reason = "VRAM 6GB 미만"
		}
	}
	return ModelChoice{Model: model, Hardware: hardware, Reason: reason}
}

func DetectHardware() Hardware {
	path, err := findNvidiaSMI()
	if err != nil {
		return Hardware{}
	}

	ctx, cancel := context.WithTimeout(context.Background(), nvidiaSMITimeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, path, "--query-gpu=name,memory.total", "--format=csv,noheader,nounits")
	hideWindow(cmd)
	output, err := cmd.Output()
	if err != nil {
		return Hardware{}
	}
	return parseNvidiaSMI(string(output))
}

func parseNvidiaSMI(output string) Hardware {
	var best Hardware
	for _, line := range strings.Split(output, "\n") {
		line = strings.TrimSpace(strings.TrimPrefix(line, "\ufeff"))
		if line == "" {
			continue
		}
		index := strings.LastIndex(line, ",")
		if index < 0 {
			continue
		}
		name := strings.TrimSpace(line[:index])
		vram, err := parseMemoryMB(line[index+1:])
		if err != nil || vram <= 0 {
			continue
		}
		if !best.Detected || vram > best.VRAMMB {
			best = Hardware{GPU: name, VRAMMB: vram, Detected: true}
		}
	}
	return best
}

func selectModel(hardware Hardware) string {
	if hardware.Detected && hardware.VRAMMB >= minVRAMFor4B {
		return Model4B
	}
	return Model1B
}

func findNvidiaSMI() (string, error) {
	if path, err := exec.LookPath("nvidia-smi.exe"); err == nil {
		return path, nil
	}
	var candidates []string
	if base := strings.TrimSpace(os.Getenv("SystemRoot")); base != "" {
		candidates = append(candidates, filepath.Join(base, "System32", "nvidia-smi.exe"))
	}
	if base := strings.TrimSpace(os.Getenv("ProgramFiles")); base != "" {
		candidates = append(candidates, filepath.Join(base, "NVIDIA Corporation", "NVSMI", "nvidia-smi.exe"))
	}
	for _, path := range candidates {
		if info, err := os.Stat(path); err == nil && !info.IsDir() {
			return path, nil
		}
	}
	return "", errors.New("nvidia-smi를 찾을 수 없습니다")
}

func parseMemoryMB(value string) (int, error) {
	value = strings.TrimSpace(value)
	digits := ""
	for _, r := range value {
		if r < '0' || r > '9' {
			break
		}
		digits += string(r)
	}
	if digits == "" {
		return 0, errors.New("메모리 값을 해석할 수 없습니다")
	}
	return strconv.Atoi(digits)
}

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
		model = DetectModel().Model
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

	report("Ollama 서버 확인 중...", -1)
	if !serverReady(ctx) {
		report("Ollama 서버 시작 중...", -1)
		if err := startServer(exe); err != nil && !serverReady(ctx) {
			return Result{}, fmt.Errorf("Ollama 서버를 시작하지 못했습니다: %w", err)
		}
		if err := waitServer(ctx); err != nil {
			return Result{}, err
		}
	}

	report(model+" 모델 확인 중...", -1)
	installed, err := hasModel(ctx, model)
	if err != nil {
		return Result{}, err
	}
	if !installed {
		report(model+" 모델 다운로드 중...", -1)
		if err := pullModel(ctx, exe, model, report); err != nil {
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
	if stopOwnedServer() {
		return
	}

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
	serverMu      sync.Mutex
	serverProcess *os.Process

	pinnedMu      sync.Mutex
	pinnedBaseURL string
	pinnedModel   string
)

func startServer(exe string) error {
	cmd := exec.Command(exe, "serve")
	hideWindow(cmd)
	cmd.Env = withEnv(os.Environ(), "OLLAMA_HOST", "127.0.0.1:11434")
	cmd.Stdout = io.Discard
	cmd.Stderr = io.Discard
	if err := cmd.Start(); err != nil {
		return err
	}

	serverMu.Lock()
	serverProcess = cmd.Process
	serverMu.Unlock()

	go func() {
		_ = cmd.Wait()
		serverMu.Lock()
		if serverProcess == cmd.Process {
			serverProcess = nil
		}
		serverMu.Unlock()
	}()
	return nil
}

func stopOwnedServer() bool {
	serverMu.Lock()
	process := serverProcess
	serverProcess = nil
	serverMu.Unlock()
	if process == nil {
		return false
	}
	killProcessTree(process)
	return true
}

func serverReady(ctx context.Context) bool {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, apiBase+"/api/tags", nil)
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

func waitServer(ctx context.Context) error {
	deadline := time.Now().Add(30 * time.Second)
	for {
		if serverReady(ctx) {
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

func hasModel(ctx context.Context, model string) (bool, error) {
	models, err := ai.ListLocalModels(ctx, BaseURL, "")
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

func pullModel(ctx context.Context, exe, model string, report func(string, int)) error {
	out := newTailWriter(16 << 10)
	out.onPercent = func(percent int) {
		report(model+" 모델 다운로드 중...", percent)
	}

	err := runProcess(ctx, exe, []string{"pull", model}, nil, out)
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
		cmd.Env = append(os.Environ(), extraEnv...)
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
	prefix := strings.ToUpper(key) + "="
	result := make([]string, 0, len(env)+1)
	for _, item := range env {
		if !strings.HasPrefix(strings.ToUpper(item), prefix) {
			result = append(result, item)
		}
	}
	return append(result, key+"="+value)
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
