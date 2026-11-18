// Package clipboard registers the global hotkey and runs the
// clipboard -> AI -> clipboard pipeline.
package clipboard

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	"golang.design/x/clipboard"
	"golang.design/x/hotkey"

	"github.com/genius/clip-translator/internal/ai"
	"github.com/genius/clip-translator/internal/config"
)

// Status describes the state shown in the GUI status bar.
type Status int

const (
	StatusIdle Status = iota
	StatusProcessing
	StatusError
)

// StatusUpdate is sent to the UI whenever the pipeline state changes.
type StatusUpdate struct {
	State   Status
	Message string
}

// Update carries the result of one pipeline run. Err is non-nil on failure.
type Update struct {
	Input  string
	Output string
	Err    error
}

// Watcher listens for the global hotkey and processes clipboard text.
type Watcher struct {
	cfg      *config.Config
	onStatus func(StatusUpdate)
	onUpdate func(Update)
	onStart  func()

	mu       sync.Mutex
	busy     bool
	hk       *hotkey.Hotkey
	stop     chan struct{}
	reload   chan struct{}
	stopOnce sync.Once
}

// New creates a watcher. The callbacks may be called from any goroutine; the
// GUI is responsible for marshalling them onto the UI thread. onStart runs only
// when clipboard text is present and a translation is actually about to begin.
func New(cfg *config.Config, onStatus func(StatusUpdate), onUpdate func(Update), onStart func()) *Watcher {
	return &Watcher{
		cfg:      cfg,
		onStatus: onStatus,
		onUpdate: onUpdate,
		onStart:  onStart,
		stop:     make(chan struct{}),
		reload:   make(chan struct{}, 1),
	}
}

// Start initializes the clipboard backend and registers the configured hotkey.
// The listener starts even if the hotkey cannot be registered, so the user can
// still change it from the GUI.
func (w *Watcher) Start() error {
	if err := clipboard.Init(); err != nil {
		return fmt.Errorf("클립보드를 사용할 수 없습니다: %w", err)
	}

	go w.listen()

	mods, key, err := parseHotkey(w.cfg.HotkeySpec())
	if err != nil {
		return err
	}

	hk := hotkey.New(mods, key)
	if err := hk.Register(); err != nil {
		return fmt.Errorf("%s 단축키 등록 실패 (다른 앱이 사용 중일 수 있습니다): %w", w.cfg.HotkeySpec(), err)
	}

	w.mu.Lock()
	w.hk = hk
	w.mu.Unlock()
	w.signalReload()

	return nil
}

// Rebind replaces the global hotkey with spec. When registration fails the
// previous hotkey is restored when possible.
func (w *Watcher) Rebind(spec string) error {
	mods, key, err := parseHotkey(spec)
	if err != nil {
		return err
	}

	w.mu.Lock()
	old := w.hk
	w.mu.Unlock()

	if old != nil {
		_ = old.Unregister()
	}

	next := hotkey.New(mods, key)
	if err := next.Register(); err != nil {
		w.restoreHotkey()
		return fmt.Errorf("%s 단축키 등록 실패 (다른 앱이 사용 중일 수 있습니다): %w", spec, err)
	}

	w.mu.Lock()
	w.hk = next
	w.mu.Unlock()
	w.signalReload()
	return nil
}

func (w *Watcher) restoreHotkey() {
	mods, key, err := parseHotkey(w.cfg.HotkeySpec())
	if err != nil {
		w.mu.Lock()
		w.hk = nil
		w.mu.Unlock()
		w.signalReload()
		return
	}

	restored := hotkey.New(mods, key)
	if err := restored.Register(); err != nil {
		w.mu.Lock()
		w.hk = nil
		w.mu.Unlock()
	} else {
		w.mu.Lock()
		w.hk = restored
		w.mu.Unlock()
	}
	w.signalReload()
}

func (w *Watcher) signalReload() {
	select {
	case w.reload <- struct{}{}:
	default:
	}
}

func (w *Watcher) listen() {
	for {
		w.mu.Lock()
		hk := w.hk
		w.mu.Unlock()

		if hk == nil {
			select {
			case <-w.stop:
				return
			case <-w.reload:
				continue
			}
		}

		select {
		case <-w.stop:
			return
		case <-w.reload:
			continue
		case _, ok := <-hk.Keydown():
			if !ok {
				select {
				case <-w.stop:
					return
				case <-w.reload:
				}
				continue
			}
			w.Trigger()
		}
	}
}

// Stop unregisters the hotkey and stops listening. It is safe to call twice.
func (w *Watcher) Stop() {
	w.stopOnce.Do(func() {
		close(w.stop)
		w.mu.Lock()
		hk := w.hk
		w.hk = nil
		w.mu.Unlock()
		if hk != nil {
			_ = hk.Unregister()
		}
	})
}

// Trigger starts one pipeline run unless one is already in progress.
func (w *Watcher) Trigger() {
	w.mu.Lock()
	if w.busy {
		w.mu.Unlock()
		return
	}
	w.busy = true
	w.mu.Unlock()

	go w.process()
}

func (w *Watcher) process() {
	defer func() {
		w.mu.Lock()
		w.busy = false
		w.mu.Unlock()
	}()

	w.status(StatusProcessing, "")

	input, err := readClipboard()
	if err != nil {
		w.fail("", err)
		return
	}
	if strings.TrimSpace(input) == "" {
		w.status(StatusIdle, "")
		return
	}

	if w.onStart != nil {
		w.onStart()
	}

	profile := w.cfg.Active()
	client := ai.New(w.cfg.APIKeyValue(), w.cfg.ModelName())

	ctx, cancel := context.WithTimeout(context.Background(), ai.DefaultTimeout)
	defer cancel()

	output, err := client.Generate(ctx, profile.Prompt, input)
	if err != nil {
		w.fail(input, err)
		return
	}

	if err := writeClipboard(output); err != nil {
		w.fail(input, fmt.Errorf("클립보드 쓰기 실패: %w", err))
		return
	}

	if w.onUpdate != nil {
		w.onUpdate(Update{Input: input, Output: output})
	}
	w.status(StatusIdle, "")
}

func (w *Watcher) fail(input string, err error) {
	if w.onUpdate != nil {
		w.onUpdate(Update{Input: input, Err: err})
	}
	w.status(StatusError, err.Error())
}

func (w *Watcher) status(state Status, message string) {
	if w.onStatus != nil {
		w.onStatus(StatusUpdate{State: state, Message: message})
	}
}

func readClipboard() (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	data, err := clipboard.Read(ctx, clipboard.FmtText)
	if errors.Is(err, clipboard.ErrNoData) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("클립보드 읽기 실패: %w", err)
	}
	return string(data), nil
}

func writeClipboard(text string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	_, err := clipboard.Write(ctx, clipboard.FmtText, []byte(text))
	return err
}

var namedKeys = map[string]hotkey.Key{
	"SPACE":   hotkey.KeySpace,
	"TAB":     hotkey.KeyTab,
	"ENTER":   hotkey.KeyReturn,
	"RETURN":  hotkey.KeyReturn,
	"ESC":     hotkey.KeyEscape,
	"ESCAPE":  hotkey.KeyEscape,
	"DELETE":  hotkey.KeyDelete,
	"DEL":     hotkey.KeyDelete,
	"LEFT":    hotkey.KeyLeft,
	"RIGHT":   hotkey.KeyRight,
	"UP":      hotkey.KeyUp,
	"DOWN":    hotkey.KeyDown,
	"PLAY":    hotkey.KeyMediaPlayPause,
	"NEXT":    hotkey.KeyMediaNext,
	"PREV":    hotkey.KeyMediaPrev,
	"STOP":    hotkey.KeyMediaStop,
	"VOLUP":   hotkey.KeyVolumeUp,
	"VOLDOWN": hotkey.KeyVolumeDown,
	"VOLMUTE": hotkey.KeyVolumeMute,
}

// ValidateHotkey reports whether spec is a supported global shortcut.
func ValidateHotkey(spec string) error {
	_, _, err := parseHotkey(spec)
	return err
}

// parseHotkey converts a spec such as "Ctrl+Alt+T" into modifiers and a key.
func parseHotkey(spec string) ([]hotkey.Modifier, hotkey.Key, error) {
	var (
		mods     []hotkey.Modifier
		seen     = map[hotkey.Modifier]bool{}
		keyToken string
	)

	addMod := func(m hotkey.Modifier) {
		if !seen[m] {
			seen[m] = true
			mods = append(mods, m)
		}
	}

	for _, raw := range strings.Split(spec, "+") {
		token := strings.ToUpper(strings.TrimSpace(raw))
		if token == "" {
			continue
		}
		switch token {
		case "CTRL", "CONTROL":
			addMod(hotkey.ModCtrl)
		case "ALT":
			addMod(hotkey.ModAlt)
		case "SHIFT":
			addMod(hotkey.ModShift)
		case "WIN", "WINDOWS", "SUPER", "META":
			addMod(hotkey.ModWin)
		default:
			if keyToken != "" {
				return nil, 0, fmt.Errorf("단축키에 키가 두 개 이상 지정되었습니다: %s", spec)
			}
			keyToken = token
		}
	}

	if keyToken == "" {
		return nil, 0, fmt.Errorf("단축키에 키가 없습니다: %s", spec)
	}
	if len(mods) == 0 {
		return nil, 0, fmt.Errorf("단축키에는 Ctrl/Alt/Shift/Win 중 하나 이상이 필요합니다: %s", spec)
	}

	key, err := parseKey(keyToken)
	if err != nil {
		return nil, 0, fmt.Errorf("지원하지 않는 단축키 키입니다 (%s): %w", keyToken, err)
	}
	return mods, key, nil
}

func parseKey(token string) (hotkey.Key, error) {
	if len(token) == 1 {
		c := token[0]
		switch {
		case c >= 'A' && c <= 'Z':
			return hotkey.Key(c), nil
		case c >= '0' && c <= '9':
			return hotkey.Key(c), nil
		}
	}

	if k, ok := namedKeys[token]; ok {
		return k, nil
	}

	if strings.HasPrefix(token, "F") {
		if n, err := strconv.Atoi(token[1:]); err == nil && n >= 1 && n <= 20 {
			return hotkey.KeyF1 + hotkey.Key(n-1), nil
		}
	}

	if n, err := strconv.ParseUint(token, 0, 32); err == nil && n > 0 {
		return hotkey.Key(n), nil
	}

	return 0, fmt.Errorf("알 수 없는 키 %q", token)
}
