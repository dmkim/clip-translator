// Command clip-translator runs the clipboard translation utility.
package main

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/tailscale/walk"

	"github.com/genius/clip-translator/internal/ai"
	"github.com/genius/clip-translator/internal/clipboard"
	"github.com/genius/clip-translator/internal/config"
	"github.com/genius/clip-translator/internal/ollama"
	"github.com/genius/clip-translator/internal/singleinstance"
	"github.com/genius/clip-translator/internal/ui"
	"github.com/genius/clip-translator/internal/update"
)

func main() {
	guard, first := singleinstance.Acquire()
	if !first {
		defer guard.Release()
		if !singleinstance.ActivateExisting() {
			singleinstance.ShowAlreadyRunningMessage()
		}
		return
	}
	defer guard.Release()

	app, err := walk.InitApp()
	if err != nil {
		log.Fatal(err)
	}
	app.SetOrganizationName("clip-translator")
	app.SetProductName("Clip Translator")

	cfg, cfgErr := config.Load()
	if cfgErr != nil {
		log.Print(cfgErr)
		walk.MsgBox(nil, "Clip Translator", cfgErr.Error()+"\n\n프로그램은 계속 실행됩니다.", walk.MsgBoxIconWarning)
	}
	if cfg == nil {
		cfg = config.NewDefault()
	}

	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		ollama.Shutdown(ctx)
	}()

	var stopLocalWarmup func()

	update.SetBeforeRestart(func() {
		if stopLocalWarmup != nil {
			stopLocalWarmup()
		}
		guard.Release()
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		ollama.Shutdown(ctx)
	})

	if cfg.ProviderName() == config.ProviderGemini {
		keepAliveCtx, stopKeepAlive := context.WithCancel(context.Background())
		defer stopKeepAlive()
		go ai.KeepAlive(keepAliveCtx, 5*time.Minute)
	}

	win, err := ui.New(cfg)
	if err != nil {
		walk.MsgBox(nil, "Clip Translator", "창을 생성하지 못했습니다.\n"+err.Error(), walk.MsgBoxIconError)
		log.Fatal(err)
	}

	watcher := clipboard.New(cfg,
		func(u clipboard.StatusUpdate) {
			win.SetStatus(u)
		},
		func(u clipboard.Update) {
			win.ShowUpdate(u)
			if u.Err != nil {
				ui.PlayError()
			} else {
				ui.PlaySuccess()
			}
		},
		func() {
			ui.PlayStart()
			win.ShowBusy()
		},
	)

	if err := watcher.Start(); err != nil {
		log.Print(err)
		win.SetStatus(clipboard.StatusUpdate{State: clipboard.StatusError, Message: err.Error()})
	}
	win.SetHotkeyApplier(func(spec string) error {
		if err := watcher.Rebind(spec); err != nil {
			return err
		}
		cfg.SetHotkey(spec)
		if err := cfg.Save(); err != nil {
			return fmt.Errorf("설정 저장 실패: %w", err)
		}
		return nil
	})
	win.SetOnClosing(watcher.Stop)
	win.CheckForUpdates()

	if cfg.ProviderName() == config.ProviderLocal {
		warmupCtx, cancelWarmup := context.WithCancel(context.Background())
		stopLocalWarmup = cancelWarmup
		defer cancelWarmup()

		go func() {
			err := ollama.Warmup(warmupCtx, cfg.LocalModelName(), nil)
			if err == nil || warmupCtx.Err() != nil {
				return
			}
			win.SetStatus(clipboard.StatusUpdate{
				State:   clipboard.StatusError,
				Message: "로컬 LLM 준비 실패: " + err.Error(),
			})
		}()
	}

	app.Run()
}
