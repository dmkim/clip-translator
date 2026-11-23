// Package ui implements the compact walk-based GUI window and audio feedback.
package ui

import (
	"bytes"
	"context"
	_ "embed"
	"fmt"
	"image/png"
	"net/url"
	"strconv"
	"strings"
	"sync/atomic"
	"syscall"
	"time"
	"unicode/utf8"

	"github.com/tailscale/walk"
	d "github.com/tailscale/walk/declarative"
	"github.com/tailscale/win"
	xclipboard "golang.design/x/clipboard"

	"github.com/genius/clip-translator/internal/ai"
	appclipboard "github.com/genius/clip-translator/internal/clipboard"
	"github.com/genius/clip-translator/internal/config"
	"github.com/genius/clip-translator/internal/update"
	"github.com/genius/clip-translator/internal/version"
)

const (
	defaultWindowWidth     = 380
	defaultWindowHeight    = 540
	defaultWindowMinHeight = 500
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
	providerRow   *walk.Composite
	providerTitle *walk.Label
	providerCombo *walk.ComboBox
	geminiRow     *walk.Composite
	localRow      *walk.Composite
	localSummary  *walk.Label

	lastOutput    string
	initializing  bool
	statusGen     uint64
	hotkeyApplier func(string) error
	onClosing     func()

	trayIcon     *walk.NotifyIcon
	trayTipShown bool
	quitting     bool
	checking     atomic.Bool
	toast        *toast
}

// windowTitle returns the caption bar text including the build version.
func windowTitle() string {
	if version.IsDev() {
		return "Clip Translator (dev)"
	}
	return "Clip Translator v" + version.Current()
}

// New builds and shows the main window.
func New(cfg *config.Config) (*Window, error) {
	w := &Window{cfg: cfg, initializing: true}

	active := cfg.Active()

	err := (d.MainWindow{
		AssignTo:        &w.mw,
		Title:           windowTitle(),
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
								Text:        "이름",
								MinSize:     d.Size{Width: 44},
								ToolTipText: "프리셋 이름 변경",
								OnClicked:   w.renameProfile,
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
				Title:  "AI 설정",
				Layout: d.VBox{Spacing: 6},
				Children: []d.Widget{
					d.Composite{
						AssignTo: &w.providerRow,
						Layout:   d.Grid{Columns: 2, Spacing: 6, MarginsZero: true},
						MinSize:  d.Size{Height: 29},
						Children: []d.Widget{
							d.Label{
								AssignTo:  &w.providerTitle,
								Text:      "제공자:",
								Column:    0,
								Alignment: d.AlignHNearVCenter,
							},
							d.ComboBox{
								AssignTo:              &w.providerCombo,
								Column:                1,
								Model:                 []string{"Gemini (클라우드)", "로컬 LLM (OpenAI 호환)"},
								Alignment:             d.AlignHNearVCenter,
								OnCurrentIndexChanged: w.onProviderChanged,
							},
						},
					},
					d.Composite{
						AssignTo: &w.geminiRow,
						Layout:   d.HBox{Spacing: 4, MarginsZero: true},
						Children: []d.Widget{
							d.LineEdit{
								AssignTo:      &w.apiKeyEdit,
								Text:          cfg.APIKeyValue(),
								PasswordMode:  true,
								CueBanner:     "Gemini API Key",
								StretchFactor: 1,
							},
							d.PushButton{
								Text:      "키 저장",
								MinSize:   d.Size{Width: 64},
								OnClicked: w.saveAPIKey,
							},
						},
					},
					d.Composite{
						AssignTo: &w.localRow,
						Layout:   d.HBox{Spacing: 4, MarginsZero: true},
						Children: []d.Widget{
							d.Label{
								AssignTo:      &w.localSummary,
								Text:          w.localSummaryText(),
								EllipsisMode:  d.EllipsisEnd,
								StretchFactor: 1,
							},
							d.PushButton{
								Text:      "설정...",
								MinSize:   d.Size{Width: 60},
								OnClicked: w.openLocalSettings,
							},
							d.PushButton{
								Text:      "테스트",
								MinSize:   d.Size{Width: 60},
								OnClicked: w.testLocalConnection,
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
	w.mw.SetExitOnClose(false)

	_ = w.providerCombo.SetCurrentIndex(providerIndex(cfg.ProviderName()))
	w.applyProviderVisibility()

	// Keep the API key cue banner visible on first run by focusing the result
	// box once the initial layout has finished.
	w.mw.RequestLayoutWithCompletionFunc(func() {
		_ = w.outEdit.SetFocus()
	})

	_ = w.initTray()
	if t, err := newToast(); err == nil {
		w.toast = t
	}

	w.initializing = false
	w.applyTopMost()
	time.AfterFunc(300*time.Millisecond, func() {
		w.post(w.fitProfileButtonHeight)
		w.post(w.fitProviderRow)
	})
	w.mw.SizeChanged().Attach(func() {
		time.AfterFunc(50*time.Millisecond, func() {
			w.post(w.fitProfileButtonHeight)
			w.post(w.fitProviderRow)
		})
	})

	w.rebuildProfileMenu()
	w.profileButton.Menu().InitPopup().Attach(func() { w.syncProfileChecks() })

	w.mw.Closing().Attach(func(canceled *bool, reason walk.CloseReason) {
		if !w.quitting && w.trayIcon != nil {
			*canceled = true
			w.hideToTray()
			return
		}
		w.disposeTray()
		if w.toast != nil {
			w.toast.dispose()
			w.toast = nil
		}
		if w.onClosing != nil {
			w.onClosing()
		}
		walk.App().Exit(0)
	})

	return w, nil
}

// SetOnClosing registers a callback invoked when the window is closing.
func (w *Window) SetOnClosing(fn func()) {
	w.onClosing = fn
}

// initTray creates the notification area icon. Tray support is optional: when
// it fails, closing the window exits the app as before.
func (w *Window) initTray() error {
	ni, err := walk.NewNotifyIcon()
	if err != nil {
		return fmt.Errorf("NewNotifyIcon: %w", err)
	}

	ok := false
	defer func() {
		if !ok {
			_ = ni.Dispose()
		}
	}()

	if err := ni.SetIcon(loadAppIcon()); err != nil {
		return fmt.Errorf("SetIcon: %w", err)
	}
	if err := ni.SetToolTip(windowTitle()); err != nil {
		return fmt.Errorf("SetToolTip: %w", err)
	}

	ni.MouseUp().Attach(func(x, y int, button walk.MouseButton) {
		if button == walk.LeftButton {
			w.showFromTray()
		}
	})
	ni.MessageClicked().Attach(w.showFromTray)

	showAction := walk.NewAction()
	_ = showAction.SetText("열기")
	showAction.Triggered().Attach(w.showFromTray)

	quitAction := walk.NewAction()
	_ = quitAction.SetText("종료")
	quitAction.Triggered().Attach(w.quit)

	actions := ni.ContextMenu().Actions()
	_ = actions.Add(showAction)
	_ = actions.Add(walk.NewSeparatorAction())
	_ = actions.Add(quitAction)

	if err := ni.SetVisible(false); err != nil {
		return fmt.Errorf("SetVisible(false): %w", err)
	}

	w.trayIcon = ni
	ok = true
	return nil
}

// hideToTray hides the window and keeps the app running from the tray.
func (w *Window) hideToTray() {
	if w.mw == nil {
		return
	}
	w.mw.SetVisible(false)

	if w.trayIcon == nil {
		return
	}
	_ = w.trayIcon.SetVisible(true)
	if !w.trayTipShown {
		w.trayTipShown = true
		_ = w.trayIcon.ShowInfo("Clip Translator",
			"프로그램이 트레이에 상주 중입니다.\n트레이 아이콘을 클릭해 다시 열거나, 우클릭 메뉴에서 종료할 수 있습니다.")
	}
}

// showFromTray restores the window and checks for a newer release.
func (w *Window) showFromTray() {
	if w.mw == nil {
		return
	}
	w.mw.SetVisible(true)
	win.ShowWindow(w.mw.Handle(), win.SW_RESTORE)
	win.SetForegroundWindow(w.mw.Handle())
	w.CheckForUpdatesFromTray()
}

// quit closes the window for real, stopping the clipboard watcher.
func (w *Window) quit() {
	if w.quitting {
		return
	}
	w.quitting = true
	go w.post(func() {
		if w.mw != nil {
			_ = w.mw.Close()
		}
	})
}

func (w *Window) disposeTray() {
	if w.trayIcon == nil {
		return
	}
	_ = w.trayIcon.SetVisible(false)
	_ = w.trayIcon.Dispose()
	w.trayIcon = nil
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

// ShowBusy displays the translucent "<preset> 실행 중" toast.
func (w *Window) ShowBusy() {
	w.post(func() {
		if w.toast == nil {
			return
		}
		w.toast.show(w.cfg.ActiveProfileName() + " · " + w.providerLabel() + " 요청 중")
	})
}

func (w *Window) hideBusy() {
	if w.toast != nil {
		w.toast.hide()
	}
}

// CheckForUpdates checks GitHub for a newer release shortly after startup and
// prompts the user to install it. It is a no-op for development builds.
func (w *Window) CheckForUpdates() {
	w.startUpdateCheck(false)
}

// CheckForUpdatesFromTray checks for updates and reports the result in the
// status label so reopening from the tray is observable.
func (w *Window) CheckForUpdatesFromTray() {
	w.startUpdateCheck(true)
}

func (w *Window) startUpdateCheck(notify bool) {
	if notify {
		w.post(func() {
			w.statusGen++
			w.statusLabel.SetText("🔄 최신 버전 확인 중...")
			_ = w.statusLabel.SetToolTipText("")
		})
	}

	go func() {
		time.Sleep(1 * time.Second)
		if !w.checking.CompareAndSwap(false, true) {
			if notify {
				w.post(func() { w.flashStatus("🟡 업데이트 확인 중입니다") })
			}
			return
		}
		defer w.checking.Store(false)
		w.checkForUpdates(notify)
	}()
}

func (w *Window) checkForUpdates(notify bool) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	rel, err := update.Check(ctx)
	if err != nil {
		if notify {
			w.post(func() { w.flashStatus("🟡 업데이트 확인 실패") })
		}
		return
	}
	if rel == nil {
		if notify {
			w.post(func() { w.flashStatus("🟢 최신 버전입니다") })
		}
		return
	}
	w.promptUpdate(rel, notify)
}

func (w *Window) promptUpdate(rel *update.Release, notify bool) {
	w.post(func() {
		if !w.mw.Visible() {
			if notify {
				w.flashStatus(w.idleText())
			}
			return
		}

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
			if notify {
				w.flashStatus(w.idleText())
			}
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

	if u.State != appclipboard.StatusProcessing {
		w.hideBusy()
	}

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

func (w *Window) renameProfile() {
	oldName := w.cfg.ActiveProfileName()

	var dlg *walk.Dialog
	var nameEdit *walk.LineEdit
	var acceptPB, cancelPB *walk.PushButton

	err := (d.Dialog{
		AssignTo:      &dlg,
		Title:         "프리셋 이름 변경",
		Size:          d.Size{Width: 340, Height: 150},
		MinSize:       d.Size{Width: 320, Height: 140},
		Layout:        d.VBox{},
		DefaultButton: &acceptPB,
		CancelButton:  &cancelPB,
		Children: []d.Widget{
			d.Label{Text: "새 프리셋 이름"},
			d.LineEdit{
				AssignTo: &nameEdit,
				Text:     oldName,
			},
			d.Composite{
				Layout: d.HBox{},
				Children: []d.Widget{
					d.HSpacer{},
					d.PushButton{
						AssignTo: &acceptPB,
						Text:     "변경",
						OnClicked: func() {
							name := strings.TrimSpace(nameEdit.Text())
							if name == "" {
								walk.MsgBox(dlg, "프리셋 이름 변경", "이름을 입력하세요.", walk.MsgBoxIconWarning)
								return
							}
							if _, err := w.cfg.RenameProfile(oldName, name); err != nil {
								walk.MsgBox(dlg, "프리셋 이름 변경", err.Error(), walk.MsgBoxIconWarning)
								return
							}
							w.rebuildProfileMenu()
							if err := w.cfg.Save(); err != nil {
								w.flashStatus("🔴 설정 저장 실패: " + err.Error())
							} else {
								w.flashStatus("🟢 프리셋 이름 변경됨")
							}
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
		walk.MsgBox(w.mw, "프리셋 이름 변경", err.Error(), walk.MsgBoxIconError)
		return
	}

	_ = nameEdit.SetFocus()
	nameEdit.SetTextSelection(0, -1)

	dlg.Run()
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

func (w *Window) fitProviderRow() {
	if w.providerRow == nil || w.providerCombo == nil || w.providerCombo.Parent() == nil {
		return
	}

	row := w.providerRow.BoundsPixels()
	bounds := w.providerCombo.BoundsPixels()
	width := row.Width - bounds.X
	changed := false

	if row.Width > 0 && width > 0 && bounds.Width != width {
		bounds.Width = width
		changed = true
	}
	if row.Height > 0 && bounds.Height > 0 {
		y := (row.Height - bounds.Height) / 2
		if bounds.Y != y {
			bounds.Y = y
			changed = true
		}
	}
	if changed {
		_ = w.providerCombo.SetBoundsPixels(bounds)
	}

	if w.providerTitle == nil || w.providerTitle.Parent() == nil {
		return
	}
	label := w.providerTitle.BoundsPixels()
	if row.Height > 0 && label.Height > 0 {
		y := (row.Height - label.Height) / 2
		if label.Y != y {
			label.Y = y
			_ = w.providerTitle.SetBoundsPixels(label)
		}
	}
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

func providerIndex(provider string) int {
	if provider == config.ProviderLocal {
		return 1
	}
	return 0
}

func (w *Window) providerLabel() string {
	if w.cfg.ProviderName() == config.ProviderLocal {
		return "로컬 LLM"
	}
	return "Gemini"
}

func (w *Window) localSummaryText() string {
	model := w.cfg.LocalModelName()
	if model == "" {
		model = "모델 미설정"
	}
	return w.cfg.LocalURL() + " · " + model
}

func (w *Window) applyProviderVisibility() {
	local := w.providerCombo != nil && w.providerCombo.CurrentIndex() == 1
	if w.geminiRow != nil {
		w.geminiRow.SetVisible(!local)
	}
	if w.localRow != nil {
		w.localRow.SetVisible(local)
	}
	if local && w.localSummary != nil {
		w.localSummary.SetText(w.localSummaryText())
	}
	if w.mw != nil {
		w.mw.RequestLayout()
		time.AfterFunc(50*time.Millisecond, func() { w.post(w.fitProviderRow) })
	}
}

func (w *Window) onProviderChanged() {
	if w.initializing {
		return
	}

	provider := config.ProviderGemini
	if w.providerCombo.CurrentIndex() == 1 {
		provider = config.ProviderLocal
	}

	w.cfg.SetAIConfig(provider, w.cfg.LocalURL(), w.cfg.LocalModelName(), w.cfg.LocalAPIKeyValue(), int(w.cfg.LocalTimeout()/time.Second))
	if err := w.cfg.Save(); err != nil {
		w.flashStatus("🔴 설정 저장 실패: " + err.Error())
		return
	}

	w.applyProviderVisibility()
	if provider == config.ProviderLocal {
		w.flashStatus("🟢 로컬 LLM 선택됨")
		return
	}
	w.flashStatus("🟢 Gemini 선택됨")
}

func validateLocalBaseURL(raw string) error {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return fmt.Errorf("Base URL을 입력하세요")
	}

	u, err := url.Parse(raw)
	if err != nil {
		return fmt.Errorf("Base URL이 올바르지 않습니다: %w", err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return fmt.Errorf("Base URL은 http:// 또는 https://로 시작해야 합니다")
	}
	if u.Host == "" {
		return fmt.Errorf("Base URL에 호스트가 없습니다")
	}
	return nil
}

func (w *Window) pingLocal(baseURL, apiKey string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	return ai.PingLocal(ctx, baseURL, apiKey)
}

func (w *Window) fetchLocalModels(baseURL, apiKey string) ([]string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	return ai.ListLocalModels(ctx, baseURL, apiKey)
}

func (w *Window) testLocalConnection() {
	baseURL := w.cfg.LocalURL()
	apiKey := w.cfg.LocalAPIKeyValue()

	w.flashStatus("🟡 로컬 LLM 연결 확인 중...")
	go func() {
		err := w.pingLocal(baseURL, apiKey)
		w.post(func() {
			if err != nil {
				w.flashStatus("🔴 로컬 LLM 연결 실패")
				walk.MsgBox(w.mw, "연결 테스트 실패", err.Error(), walk.MsgBoxIconError)
				return
			}
			w.flashStatus("🟢 로컬 LLM 연결 성공")
		})
	}()
}

func (w *Window) openLocalSettings() {
	var dlg *walk.Dialog
	var baseEdit, keyEdit, timeoutEdit *walk.LineEdit
	var modelEdit *walk.ComboBox
	var acceptPB, cancelPB, testPB, loadModelsPB *walk.PushButton

	err := (d.Dialog{
		AssignTo:      &dlg,
		Title:         "로컬 LLM 설정",
		Size:          d.Size{Width: 380, Height: 300},
		MinSize:       d.Size{Width: 360, Height: 280},
		Layout:        d.VBox{Spacing: 6},
		DefaultButton: &acceptPB,
		CancelButton:  &cancelPB,
		Children: []d.Widget{
			d.Label{Text: "Base URL (예: http://localhost:11434/v1)"},
			d.LineEdit{
				AssignTo:  &baseEdit,
				Text:      w.cfg.LocalURL(),
				CueBanner: "http://localhost:11434/v1",
			},
			d.Label{Text: "모델 이름 (예: qwen2.5:7b-instruct)"},
			d.Composite{
				Layout: d.HBox{Spacing: 4, MarginsZero: true},
				Children: []d.Widget{
					d.ComboBox{
						AssignTo:      &modelEdit,
						Editable:      true,
						StretchFactor: 1,
						Model:         []string{},
					},
					d.PushButton{
						AssignTo: &loadModelsPB,
						Text:     "불러오기",
						MinSize:  d.Size{Width: 72},
						OnClicked: func() {
							baseURL := strings.TrimSpace(baseEdit.Text())
							if err := validateLocalBaseURL(baseURL); err != nil {
								walk.MsgBox(dlg, "모델 불러오기", err.Error(), walk.MsgBoxIconWarning)
								return
							}

							apiKey := strings.TrimSpace(keyEdit.Text())
							loadModelsPB.SetEnabled(false)
							go func() {
								models, err := w.fetchLocalModels(baseURL, apiKey)
								dlg.Synchronize(func() {
									loadModelsPB.SetEnabled(true)
									if err != nil {
										walk.MsgBox(dlg, "모델 불러오기 실패", err.Error(), walk.MsgBoxIconError)
										return
									}
									if len(models) == 0 {
										walk.MsgBox(dlg, "모델 불러오기", "사용 가능한 모델이 없습니다.", walk.MsgBoxIconWarning)
										return
									}

									current := strings.TrimSpace(modelEdit.Text())
									_ = modelEdit.SetModel(models)
									index := 0
									for i, name := range models {
										if name == current {
											index = i
											break
										}
									}
									_ = modelEdit.SetCurrentIndex(index)
									_ = modelEdit.SetText(models[index])
									if len(models) > 1 {
										walk.MsgBox(dlg, "모델 불러오기", fmt.Sprintf("%d개 모델을 불러왔습니다. 드롭다운에서 선택하세요.", len(models)), walk.MsgBoxIconInformation)
									}
								})
							}()
						},
					},
				},
			},
			d.Label{Text: "API Key (선택 사항)"},
			d.LineEdit{
				AssignTo:     &keyEdit,
				Text:         w.cfg.LocalAPIKeyValue(),
				PasswordMode: true,
				CueBanner:    "필요한 경우에만 입력",
			},
			d.Label{Text: "타임아웃 (초)"},
			d.LineEdit{
				AssignTo:  &timeoutEdit,
				Text:      strconv.Itoa(int(w.cfg.LocalTimeout() / time.Second)),
				CueBanner: "120",
			},
			d.Composite{
				Layout: d.HBox{Spacing: 4},
				Children: []d.Widget{
					d.PushButton{
						AssignTo: &testPB,
						Text:     "연결 테스트",
						OnClicked: func() {
							baseURL := strings.TrimSpace(baseEdit.Text())
							if err := validateLocalBaseURL(baseURL); err != nil {
								walk.MsgBox(dlg, "연결 테스트", err.Error(), walk.MsgBoxIconWarning)
								return
							}

							apiKey := strings.TrimSpace(keyEdit.Text())
							testPB.SetEnabled(false)
							go func() {
								err := w.pingLocal(baseURL, apiKey)
								dlg.Synchronize(func() {
									testPB.SetEnabled(true)
									if err != nil {
										walk.MsgBox(dlg, "연결 테스트 실패", err.Error(), walk.MsgBoxIconError)
										return
									}
									walk.MsgBox(dlg, "연결 테스트", "로컬 LLM 서버에 연결할 수 있습니다.", walk.MsgBoxIconInformation)
								})
							}()
						},
					},
					d.HSpacer{},
					d.PushButton{
						AssignTo: &acceptPB,
						Text:     "저장",
						OnClicked: func() {
							if err := validateLocalBaseURL(baseEdit.Text()); err != nil {
								walk.MsgBox(dlg, "로컬 LLM 설정", err.Error(), walk.MsgBoxIconWarning)
								return
							}
							timeoutSeconds, err := strconv.Atoi(strings.TrimSpace(timeoutEdit.Text()))
							if err != nil || timeoutSeconds <= 0 {
								walk.MsgBox(dlg, "로컬 LLM 설정", "타임아웃은 1초 이상의 숫자로 입력하세요.", walk.MsgBoxIconWarning)
								return
							}

							w.cfg.SetAIConfig(config.ProviderLocal, baseEdit.Text(), modelEdit.Text(), keyEdit.Text(), timeoutSeconds)
							if err := w.cfg.Save(); err != nil {
								walk.MsgBox(dlg, "로컬 LLM 설정", "설정 저장 실패: "+err.Error(), walk.MsgBoxIconError)
								return
							}

							w.applyProviderVisibility()
							w.flashStatus("🟢 로컬 LLM 설정 저장됨")
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
		walk.MsgBox(w.mw, "로컬 LLM 설정", err.Error(), walk.MsgBoxIconError)
		return
	}

	_ = modelEdit.SetText(w.cfg.LocalModelName())
	dlg.Run()
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
