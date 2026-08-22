// Copyright (C) 2026 glowinthedark
//
// SPDX-License-Identifier: GPL-3.0-or-later

//go:build !android && (darwin || linux || windows)

package tray

import (
	_ "embed"
	"runtime"

	"github.com/gogpu/systray"
)

// The mark, generated from internal/server/web/favicon.svg by
// tools/make-icons.sh (`make icons`) and committed, so `make build` needs no
// image toolchain. D70: the mark is never redrawn, only rendered.
//
// GOOS=android satisfies the linux build constraint too, so the !android above
// is what keeps godbus and goffi out of the APK — no new build tag, no second
// CI dimension.
var (
	//go:embed icons/tray.png
	iconPNG []byte
	//go:embed icons/tray-template.png
	iconTemplatePNG []byte
)

type systrayPlatform struct{ t *systray.SystemTray }

func newPlatform() platform { return &systrayPlatform{} }

// Start always returns nil, and that is the library's fault rather than an
// oversight: New(), SetIcon(), SetMenu() and Show() all discard their platform
// errors, so there is nothing here to report. preflight is the check that
// actually decides whether this will work.
func (p *systrayPlatform) Start(cfg Config, items []Item) error {
	menu := systray.NewMenu()
	for _, it := range items {
		if it.Sep {
			menu.AddSeparator()
			continue
		}
		do := it.Do
		if do == nil {
			// The version header has no action. A nil callback reaching the
			// FFI layer is not worth finding out about the hard way.
			do = func() {}
		}
		mi := menu.Add(it.Label, do)
		if it.Disabled {
			mi.SetDisabled(true)
		}
	}

	t := systray.New()
	if runtime.GOOS == "darwin" {
		// A template image is drawn by the system from its alpha channel
		// alone, so it follows the menu bar through light and dark mode.
		t.SetTemplateIcon(iconTemplatePNG)
	} else {
		t.SetIcon(iconPNG)
	}
	t.SetTooltip(cfg.tooltip()).SetMenu(menu).Show()
	p.t = t
	return nil
}

func (p *systrayPlatform) Run() error {
	if p.t == nil {
		return errNoTray
	}
	return p.t.Run()
}

func (p *systrayPlatform) Stop() {
	if p.t != nil {
		p.t.Remove()
	}
}
