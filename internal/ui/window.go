// Package ui implements the compact walk-based GUI window and audio feedback.
package ui

import (
	"bytes"
	"context"
	_ "embed"
	"fmt"
	"image/png"
	"strings"
	"syscall"
	"time"
	"unicode/utf8"

	"github.com/tailscale/walk"
	d "github.com/tailscale/walk/declarative"
	"github.com/tailscale/win"
	xclipboard "golang.design/x/clipboard"

	appclipboard "github.com/genius/clip-translator/internal/clipboard"
	"github.com/genius/clip-translator/internal/config"
	"github.com/genius/clip-translator/internal/update"
	"github.com/genius/clip-translator/internal/version"
)

const (
	defaultWindowWidth     = 380
	defaultWindowHeight    = 500
	defaultWindowMinHeight = 470
	newProfilePrompt       = "Translate the user's input into natural, idiomatic English. Output ONLY the translation."

	// toolIconSize is the rendered size of the gear/help icons in pixels at
	// 96 DPI. Change this value (e.g. 12, 14, 16, 18) to resize both icons.
	toolIconSize = 10
)

//go:embed assets/gear.png
var gearPNG []byte

//go:embed assets/help.png
var helpPNG []byte

//go:embed assets/appicon.png
var appIconPNG []byte

// loadAppIcon decodes the embedded application icon for the window title bar.
func loadAppIcon() *walk.Icon {
	img, err := png.Decode(bytes.NewReader(appIconPNG))
	if err != nil {
		return nil
	}
	icon, err := walk.NewIconFromImage(img)
	if err != nil {
		return nil
	}
	return icon
}

// loadGearIcon decodes the embedded gear image at toolIconSize.
func loadGearIcon() *walk.Icon {
	return loadToolIcon(gearPNG)
}

// loadHelpIcon decodes the embedded help image at toolIconSize.
func loadHelpIcon() *walk.Icon {
	return loadToolIcon(helpPNG)
}

// loadToolIcon decodes a PNG and renders it at toolIconSize, scaling the
// source image for the current DPI.
func loadToolIcon(data []byte) *walk.Icon {
	img, err := png.Decode(bytes.NewReader(data))
	if err != nil {
		return nil
	}

	b := img.Bounds()
	source := b.Dx()
	if b.Dy() > source {
		source = b.Dy()
	}
	if source <= 0 {
		return nil
	}

	// NewIconFromImageForDPI scales by 96/dpi, so pick the DPI that yields
	// toolIconSize pixels at 96 DPI.
	dpi := source * 96 / toolIconSize
	if dpi < 1 {
		dpi = 1
	}

	icon, err := walk.NewIconFromImageForDPI(img, dpi)
	if err != nil {
		return nil
	}
	return icon
}

// Window is the main application window.
type Window struct {
	cfg *config.Config
	mw  *walk.MainWindow

	statusLabel   *walk.Label
	topCheck      *walk.CheckBox
	profileButton *walk.SplitButton
	promptEdit    *walk.TextEdit
	inLabel       *walk.Label
	outEdit       *walk.TextEdit
	apiKeyEdit    *walk.LineEdit

	lastOutput    string
	initializing  bool
	statusGen     uint64
	hotkeyApplier func(string) error
	onClosing     func()
}

// New builds and shows the main window.
func New(cfg *config.Config) (*Window, error) {
	w := &Window{cfg: cfg, initializing: true}

	active := cfg.Active()

	err := (d.MainWindow{
		AssignTo:        &w.mw,
		Title:           "Clip Translator",
		Icon:            loadAppIcon(),
		Size:            d.Size{Width: defaultWindowWidth, Height: defaultWindowHeight},
		MinSize:         d.Size{Width: defaultWindowWidth, Height: defaultWindowMinHeight},
		DisableMaximize: true,
		Layout:          d.VBox{Spacing: 6},
		Children: []d.Widget{
			d.Composite{
				Layout:  d.HBox{MarginsZero: true, Spacing: 4},
				MinSize: d.Size{Height: 24},
				Children: []d.Widget{
					d.Label{
						AssignTo:     &w.statusLabel,
						Text:         w.idleText(),
						EllipsisMode: d.EllipsisEnd,
					},
					d.ImageView{
						Image:       loadGearIcon(),
						Mode:        d.ImageViewModeIdeal,
						Margin:      3,
						ToolTipText: "단축키 변경",
						OnMouseUp: func(x, y int, button walk.MouseButton) {
							if button == walk.LeftButton {
								w.changeHotkey()
							}
						},
					},
					d.HSpacer{},
					d.CheckBox{
						AssignTo:         &w.topCheck,
						Text:             "항상 위",
						Checked:          cfg.IsAlwaysOnTop(),
						OnCheckedChanged: w.onTopMostChanged,
					},
					d.ImageView{
						Image:       loadHelpIcon(),
						Mode:        d.ImageViewModeIdeal,
						Margin:      3,
						ToolTipText: "도움말",
						OnMouseUp: func(x, y int, button walk.MouseButton) {
							if button == walk.LeftButton {
								w.showHelp()
							}
						},
					},
				},
			},
			d.GroupBox{
				Title:  "프리셋",
				Layout: d.VBox{Spacing: 6},
				Children: []d.Widget{
					d.Composite{
						Layout: d.HBox{Spacing: 4, MarginsZero: true},
						Children: []d.Widget{
							d.SplitButton{
								AssignTo: &w.profileButton,
								Text:     cfg.ActiveProfileName(),
								MinSize:  d.Size{Width: 160},
							},
							d.HSpacer{},
							d.PushButton{
								Text:      "+ 추가",
								MinSize:   d.Size{Width: 56},
								OnClicked: w.addProfile,
							},
							d.PushButton{
								Text:      "삭제",
								MinSize:   d.Size{Width: 44},
								OnClicked: w.removeProfile,
							},
						},
					},
					d.TextEdit{
						AssignTo:      &w.promptEdit,
						Text:          active.Prompt,
						VScroll:       true,
						MinSize:       d.Size{Height: 56},
						StretchFactor: 1,
					},
					d.PushButton{
						Text:      "프롬프트 저장",
						OnClicked: w.savePrompt,
					},
				},
			},
			d.GroupBox{
				Title:  "최근 결과",
				Layout: d.VBox{Spacing: 6},
				Children: []d.Widget{
					d.Label{
						AssignTo:     &w.inLabel,
						Text:         "입력 (IN): -",
						EllipsisMode: d.EllipsisEnd,
					},
					d.Composite{
						Layout: d.HBox{Spacing: 4, MarginsZero: true},
						Children: []d.Widget{
							d.Label{Text: "결과 (OUT):"},
							d.HSpacer{},
							d.PushButton{
								Text:      "다시 복사",
								MinSize:   d.Size{Width: 80},
								OnClicked: w.copyResult,
							},
						},
					},
					d.TextEdit{
						AssignTo:      &w.outEdit,
						ReadOnly:      true,
						VScroll:       true,
						MinSize:       d.Size{Height: 48},
						StretchFactor: 1,
					},
				},
			},
			d.GroupBox{
				Title:  "API 설정",
				Layout: d.VBox{},
				Children: []d.Widget{
					d.Composite{
						Layout: d.HBox{Spacing: 4, MarginsZero: true},
						Children: []d.Widget{
							d.LineEdit{
								AssignTo:     &w.apiKeyEdit,
								Text:         cfg.APIKeyValue(),
								PasswordMode: true,
								CueBanner:    "Gemini API Key",
							},
							d.PushButton{
								Text:      "키 저장",
								MinSize:   d.Size{Width: 64},
								OnClicked: w.saveAPIKey,
							},
						},
					},
				},
			},
		},
	}).Create()
	if err != nil {
		return nil, err
	}

	// walk always creates a toolbar and a status bar for a MainWindow. The empty
	// toolbar is painted at the top of the client area and covers the first row
	// because the layout only reserves toolbar space when it has actions.
	w.mw.ToolBar().SetVisible(false)
	w.mw.StatusBar().SetVisible(false)

	// Keep the API key cue banner visible on first run by focusing the result
	// box once the initial layout has finished.
	w.mw.RequestLayoutWithCompletionFunc(func() {
		_ = w.outEdit.SetFocus()
	})

	w.initializing = false
	w.applyTopMost()
	time.AfterFunc(300*time.Millisecond, func() { w.post(w.fitProfileButtonHeight) })
	w.mw.SizeChanged().Attach(func() {
		time.AfterFunc(50*time.Millisecond, func() { w.post(w.fitProfileButtonHeight) })
	})

	w.rebuildProfileMenu()
	w.profileButton.Menu().InitPopup().Attach(func() { w.syncProfileChecks() })

	w.mw.Closing().Attach(func(*bool, walk.CloseReason) {
		if w.onClosing != nil {
			w.onClosing()
		}
	})

	return w, nil
}

// SetOnClosing registers a callback invoked when the window is closing.
func (w *Window) SetOnClosing(fn func()) {
	w.onClosing = fn
}

// SetHotkeyApplier registers the callback used to apply a new global shortcut.
func (w *Window) SetHotkeyApplier(fn func(string) error) {
	w.hotkeyApplier = fn
}

// SetStatus applies a state update from the clipboard watcher on the UI thread.
func (w *Window) SetStatus(u appclipboard.StatusUpdate) {
	w.post(func() { w.applyStatus(u) })
}

// ShowUpdate applies a pipeline result on the UI thread.
func (w *Window) ShowUpdate(u appclipboard.Update) {
	w.post(func() { w.applyUpdate(u) })
}

// CheckForUpdates checks GitHub for a newer release shortly after startup and
// prompts the user to install it. It is a no-op for development builds.
func (w *Window) CheckForUpdates() {
	go func() {
		time.Sleep(3 * time.Second)
		w.checkForUpdates()
	}()
}

func (w *Window) checkForUpdates() {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	rel, err := update.Check(ctx)
	if err != nil || rel == nil {
		return
	}
	w.promptUpdate(rel)
}

func (w *Window) promptUpdate(rel *update.Release) {
	w.post(func() {
		notes := rel.Notes
		if notes == "" {
			notes = "릴리스 노트가 없습니다."
		}

		message := fmt.Sprintf(
			"새 버전 v%s이(가) 있습니다. (현재 v%s)\n\n%s\n\n지금 업데이트할까요?",
			rel.Version, version.Current(), notes,
		)
		if walk.MsgBox(w.mw, "업데이트 사용 가능", message,
			walk.MsgBoxYesNo|walk.MsgBoxIconInformation) != walk.DlgCmdYes {
			return
		}

		w.installUpdate(rel)
	})
}

func (w *Window) installUpdate(rel *update.Release) {
	w.statusGen++
	w.statusLabel.SetText("🔵 업데이트 다운로드 중...")
	_ = w.statusLabel.SetToolTipText("")

	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
		defer cancel()

		if err := update.Apply(ctx, rel); err != nil {
			w.post(func() {
				w.flashStatus("🔴 업데이트 실패")
				walk.MsgBox(w.mw, "업데이트 실패",
					err.Error()+"\n\nGitHub 릴리스에서 직접 내려받아 주세요.\n"+rel.URL,
					walk.MsgBoxIconError)
			})
			return
		}

		w.post(func() {
			walk.MsgBox(w.mw, "업데이트 완료",
				"새 버전이 설치되었습니다. 프로그램을 다시 시작합니다.",
				walk.MsgBoxIconInformation)
			if err := update.Restart(); err != nil {
				walk.MsgBox(w.mw, "재시작 실패",
					err.Error()+"\n프로그램을 직접 다시 실행해 주세요.",
					walk.MsgBoxIconError)
			}
		})
	}()
}

func (w *Window) applyStatus(u appclipboard.StatusUpdate) {
	w.statusGen++

	switch u.State {
	case appclipboard.StatusProcessing:
		w.statusLabel.SetText("🟡 처리 중...")
		_ = w.statusLabel.SetToolTipText("")
	case appclipboard.StatusError:
		message := u.Message
		if message == "" {
			message = "알 수 없는 오류가 발생했습니다"
		}
		w.statusLabel.SetText("🔴 오류 발생")
		_ = w.statusLabel.SetToolTipText(message)
	default:
		w.statusLabel.SetText(w.idleText())
		_ = w.statusLabel.SetToolTipText("")
	}
}

func (w *Window) applyUpdate(u appclipboard.Update) {
	if u.Input != "" {
		w.inLabel.SetText("입력 (IN): " + summarize(u.Input, 70))
	}

	if u.Err != nil {
		w.lastOutput = ""
		w.outEdit.SetText("⚠ " + u.Err.Error())
		return
	}

	w.lastOutput = u.Output
	w.outEdit.SetText(u.Output)
}

func (w *Window) idleText() string {
	return "🟢 대기 중 (" + w.cfg.HotkeySpec() + ")"
}

func (w *Window) onTopMostChanged() {
	if w.initializing {
		return
	}
	w.cfg.SetAlwaysOnTop(w.topCheck.Checked())
	w.applyTopMost()

	if err := w.cfg.Save(); err != nil {
		w.flashStatus("🔴 설정 저장 실패: " + err.Error())
	}
}

func (w *Window) selectProfile(name string) {
	if !w.cfg.SetActiveProfile(name) {
		return
	}
	w.profileButton.SetText(name)
	if p, ok := w.cfg.Profile(name); ok {
		w.promptEdit.SetText(p.Prompt)
	}
	if err := w.cfg.Save(); err != nil {
		w.flashStatus("🔴 설정 저장 실패: " + err.Error())
	}
}

func (w *Window) rebuildProfileMenu() {
	actions := w.profileButton.Menu().Actions()
	_ = actions.Clear()

	active := w.cfg.ActiveProfileName()
	for _, name := range w.cfg.ProfileNames() {
		n := name
		action := walk.NewAction()
		_ = action.SetText(n)
		_ = action.SetCheckable(true)
		_ = action.SetChecked(n == active)
		action.Triggered().Attach(func() { w.selectProfile(n) })
		_ = actions.Add(action)
	}
	w.profileButton.SetText(active)
}

func (w *Window) syncProfileChecks() {
	active := w.cfg.ActiveProfileName()
	actions := w.profileButton.Menu().Actions()
	for i := 0; i < actions.Len(); i++ {
		action := actions.At(i)
		_ = action.SetChecked(action.Text() == active)
	}
}

func (w *Window) addProfile() {
	p := w.cfg.AddProfile("새 프리셋", newProfilePrompt)
	if err := w.cfg.Save(); err != nil {
		w.flashStatus("🔴 설정 저장 실패: " + err.Error())
	}
	w.reloadProfiles(p.Name)
	w.flashStatus("🟢 프리셋 추가됨")
}

func (w *Window) removeProfile() {
	name := w.cfg.ActiveProfileName()
	active, err := w.cfg.RemoveProfile(name)
	if err != nil {
		w.flashStatus("🔴 " + err.Error())
		return
	}
	if err := w.cfg.Save(); err != nil {
		w.flashStatus("🔴 설정 저장 실패: " + err.Error())
	}
	w.reloadProfiles(active.Name)
	w.flashStatus("🟢 프리셋 삭제됨")
}

func (w *Window) reloadProfiles(active string) {
	w.rebuildProfileMenu()

	if p, ok := w.cfg.Profile(active); ok {
		w.promptEdit.SetText(p.Prompt)
	}
}

// fitProfileButtonHeight stretches the split button to the height of the
// neighboring push buttons, which include an extra theme margin that the
// split button's native control does not account for.
func (w *Window) fitProfileButtonHeight() {
	if w.profileButton == nil || w.profileButton.Parent() == nil {
		return
	}

	rowHeight := w.profileButton.Parent().BoundsPixels().Height
	bounds := w.profileButton.BoundsPixels()
	if rowHeight <= 0 || bounds.Height == rowHeight {
		return
	}

	bounds.Y = 0
	bounds.Height = rowHeight
	_ = w.profileButton.SetBoundsPixels(bounds)
}

func (w *Window) savePrompt() {
	name := w.cfg.ActiveProfileName()
	if !w.cfg.SetPrompt(name, w.promptEdit.Text()) {
		w.flashStatus("🔴 프리셋을 찾을 수 없습니다")
		return
	}
	if err := w.cfg.Save(); err != nil {
		w.flashStatus("🔴 설정 저장 실패: " + err.Error())
		return
	}
	w.flashStatus("🟢 프롬프트 저장됨")
}

func (w *Window) saveAPIKey() {
	key := strings.TrimSpace(w.apiKeyEdit.Text())
	if key == "" {
		w.flashStatus("🔴 API 키를 입력하세요")
		return
	}
	w.cfg.SetAPIKey(key)
	if err := w.cfg.Save(); err != nil {
		w.flashStatus("🔴 설정 저장 실패: " + err.Error())
		return
	}
	w.flashStatus("🟢 API 키 저장됨")
}

func (w *Window) showHelp() {
	message := fmt.Sprintf(
		"1. 번역할 텍스트를 선택한 뒤 Ctrl+C 또는 Ctrl+X로 복사합니다.\n\n"+
			"2. 어디서든 %s 단축키를 누르면 번역이 시작됩니다.\n\n"+
			"3. 결과가 창에 표시되고 클립보드에 자동으로 복사됩니다.\n\n"+
			"4. 원하는 곳에서 Ctrl+V로 붙여넣습니다.\n\n"+
			"알림음으로 진행 상태를 확인할 수 있습니다.\n"+
			"- 시작: 523→659Hz 상승 두 음\n"+
			"- 성공: 800Hz\n"+
			"- 실패: 300Hz",
		w.cfg.HotkeySpec(),
	)
	walk.MsgBox(w.mw, "사용 방법", message, walk.MsgBoxIconInformation)
}

func (w *Window) changeHotkey() {
	if w.hotkeyApplier == nil {
		return
	}

	var dlg *walk.Dialog
	var hotkeyEdit *walk.LineEdit
	var acceptPB, cancelPB *walk.PushButton

	err := (d.Dialog{
		AssignTo:      &dlg,
		Title:         "단축키 변경",
		Size:          d.Size{Width: 340, Height: 160},
		MinSize:       d.Size{Width: 320, Height: 150},
		Layout:        d.VBox{},
		DefaultButton: &acceptPB,
		CancelButton:  &cancelPB,
		Children: []d.Widget{
			d.Label{Text: "새 단축키 (예: Ctrl+Alt+T)"},
			d.LineEdit{
				AssignTo:  &hotkeyEdit,
				Text:      w.cfg.HotkeySpec(),
				CueBanner: "Ctrl+Alt+T",
			},
			d.Label{Text: "Ctrl/Alt/Shift/Win + A-Z, 0-9, F1-F20 등"},
			d.Composite{
				Layout: d.HBox{},
				Children: []d.Widget{
					d.HSpacer{},
					d.PushButton{
						AssignTo: &acceptPB,
						Text:     "적용",
						OnClicked: func() {
							spec := strings.TrimSpace(hotkeyEdit.Text())
							if spec == "" {
								walk.MsgBox(dlg, "단축키 변경", "단축키를 입력하세요.", walk.MsgBoxIconWarning)
								return
							}
							if err := appclipboard.ValidateHotkey(spec); err != nil {
								walk.MsgBox(dlg, "단축키 변경", err.Error(), walk.MsgBoxIconWarning)
								return
							}
							if err := w.hotkeyApplier(spec); err != nil {
								walk.MsgBox(dlg, "단축키 변경 실패", err.Error(), walk.MsgBoxIconError)
								return
							}
							w.flashStatus("🟢 단축키 변경됨")
							dlg.Accept()
						},
					},
					d.PushButton{
						AssignTo:  &cancelPB,
						Text:      "취소",
						OnClicked: func() { dlg.Cancel() },
					},
				},
			},
		},
	}).Create(w.mw)
	if err != nil {
		walk.MsgBox(w.mw, "단축키 변경", err.Error(), walk.MsgBoxIconError)
		return
	}

	dlg.Run()
}

func (w *Window) copyResult() {
	text := w.lastOutput
	if text == "" {
		text = w.outEdit.Text()
	}
	if strings.TrimSpace(text) == "" {
		return
	}

	go func() {
		if err := writeClipboard(text); err != nil {
			w.SetStatus(appclipboard.StatusUpdate{State: appclipboard.StatusError, Message: err.Error()})
			PlayError()
			return
		}
		PlaySuccess()
	}()
}

func (w *Window) flashStatus(text string) {
	w.statusGen++
	gen := w.statusGen
	w.statusLabel.SetText(text)

	time.AfterFunc(1500*time.Millisecond, func() {
		w.post(func() {
			if w.statusGen != gen {
				return
			}
			w.statusLabel.SetText(w.idleText())
		})
	})
}

func (w *Window) post(fn func()) {
	if w.mw == nil {
		return
	}
	w.mw.Synchronize(fn)
}

func (w *Window) applyTopMost() {
	after := win.HWND_NOTOPMOST
	if w.topCheck.Checked() {
		after = win.HWND_TOPMOST
	}
	win.SetWindowPos(w.mw.Handle(), after, 0, 0, 0, 0,
		win.SWP_NOMOVE|win.SWP_NOSIZE|win.SWP_NOACTIVATE)
}

func writeClipboard(text string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	_, err := xclipboard.Write(ctx, xclipboard.FmtText, []byte(text))
	return err
}

func summarize(text string, max int) string {
	flat := strings.Join(strings.Fields(text), " ")
	if utf8.RuneCountInString(flat) <= max {
		return flat
	}
	runes := []rune(flat)
	return string(runes[:max]) + "…"
}

var (
	kernel32 = syscall.NewLazyDLL("kernel32.dll")
	beepProc = kernel32.NewProc("Beep")
)

// PlayStart emits the start chime: two short rising notes (523 Hz, 659 Hz).
func PlayStart() {
	go func() {
		_, _, _ = beepProc.Call(523, 80)
		time.Sleep(40 * time.Millisecond)
		_, _, _ = beepProc.Call(659, 80)
	}()
}

// PlaySuccess emits the success beep (800 Hz, 120 ms).
func PlaySuccess() {
	playBeep(800, 120)
}

// PlayError emits the failure beep (300 Hz, 250 ms).
func PlayError() {
	playBeep(300, 250)
}

func playBeep(freq, duration uintptr) {
	go func() {
		_, _, _ = beepProc.Call(freq, duration)
	}()
}
