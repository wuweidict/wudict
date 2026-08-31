// Copyright (C) 2026 glowinthedark
//
// SPDX-License-Identifier: GPL-3.0-or-later

package server

import (
	"strings"
	"testing"

	"github.com/wuweidict/wudict/internal/htmlref"
)

// DSL now emits pronunciation as a link (D81), but <object type="audio/…"> is
// still in the wild: every library folder prepared before that change stores
// it, and it is what GoldenDict-era sources spell. `clean` drops <object> as
// an embedding vector, so the URL has to be rescued into a plain <audio> or
// those dictionaries lose their audio for good.
func TestCleanRescuesLegacyAudioObject(t *testing.T) {
	const base = "http://example.com"
	cases := []struct {
		name, in, want string
	}{
		{"dsl legacy", `<object type="audio/x-wav" data="/res/abc/beat.mp3" width="40" height="40"><param name="autoplay" value="false" /></object>`,
			`<audio src="http://example.com/res/abc/beat.mp3" controls="controls"></audio>`},
		{"already absolute", `<object type="audio/mpeg" data="https://cdn.example.org/a.mp3"></object>`,
			`<audio src="https://cdn.example.org/a.mp3" controls="controls"></audio>`},
	}
	for _, c := range cases {
		got := applyFormat(c.in, "clean", base, htmlref.Styles{})
		if !strings.Contains(got, c.want) {
			t.Errorf("%s: applyFormat = %q, want it to contain %q", c.name, got, c.want)
		}
		if strings.Contains(got, "<object") {
			t.Errorf("%s: kept an <object>: %q", c.name, got)
		}
	}
	// A non-audio <object> is an embedding vector with nothing to rescue.
	if got := applyFormat(`<object data="x.swf"><p>fallback</p></object>`, "clean", base, htmlref.Styles{}); strings.Contains(got, "object") || strings.Contains(got, "audio") {
		t.Errorf("non-audio object survived: %q", got)
	}
}
