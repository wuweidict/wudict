// Copyright (C) 2026 glowinthedark
//
// SPDX-License-Identifier: GPL-3.0-or-later

package intake

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"testing"
	"time"
)

// TestMegaFetch serves an encrypted file the way MEGA does and checks that
// Fetch resolves the link, names the file from its attributes and decrypts it.
func TestMegaFetch(t *testing.T) {
	linkKey := make([]byte, 32)
	for i := range linkKey {
		linkKey[i] = byte(i*7 + 3)
	}
	key := make([]byte, 16)
	for i := range key {
		key[i] = linkKey[i] ^ linkKey[i+16]
	}
	block, _ := aes.NewCipher(key)

	plain := bytes.Repeat([]byte("0123456789abcdefXYZ"), 5000) // not a block multiple
	enc := make([]byte, len(plain))
	iv := append(append([]byte{}, linkKey[16:24]...), make([]byte, 8)...)
	cipher.NewCTR(block, iv).XORKeyStream(enc, plain)

	attr := []byte(`MEGA{"n":"oxford.mdx"}`)
	attr = append(attr, make([]byte, (16-len(attr)%16)%16)...)
	cipher.NewCBCEncrypter(block, make([]byte, 16)).CryptBlocks(attr, attr)

	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/cs":
			var req []map[string]any
			if json.NewDecoder(r.Body).Decode(&req) != nil || req[0]["p"] != "AbCd_123" {
				_, _ = io.WriteString(w, "[-9]")
				return
			}
			_ = json.NewEncoder(w).Encode([]map[string]any{{
				"s": len(enc), "g": srv.URL + "/dl", "at": base64.RawURLEncoding.EncodeToString(attr),
			}})
		case "/dl":
			http.ServeContent(w, r, "", time.Time{}, bytes.NewReader(enc))
		}
	}))
	defer srv.Close()
	old := megaAPI
	megaAPI = srv.URL + "/cs"
	defer func() { megaAPI = old }()

	f := Fetcher{Insecure: true}
	k := base64.RawURLEncoding.EncodeToString(linkKey)
	for _, link := range []string{"https://mega.nz/file/AbCd_123#" + k, "https://mega.nz/#!AbCd_123!" + k,
		"https://legbehindneck.com/wudict#https://mega.nz/file/AbCd_123%23" + k} {
		src, err := f.Fetch(context.Background(), t.TempDir(), Unwrap(link), nil)
		if err != nil {
			t.Fatalf("%s: %v", link, err)
		}
		got, _ := os.ReadFile(src.Path)
		if src.Name != "oxford.mdx" || !bytes.Equal(got, plain) {
			t.Fatalf("%s: name %q, %d bytes, equal %v", link, src.Name, len(got), bytes.Equal(got, plain))
		}
	}

	// A resume mid-block continues the keystream where it stopped.
	u, _ := url.Parse("https://mega.nz/file/AbCd_123#" + k)
	m, err := f.megaResolve(context.Background(), "AbCd_123", linkKey)
	if err != nil {
		t.Fatal(err)
	}
	const off = 1000 + 5
	tail, _ := io.ReadAll(m.reader(bytes.NewReader(enc[off:]), off))
	if !bytes.Equal(tail, plain[off:]) || partBase(u) != "mega-AbCd_123" {
		t.Fatal("resume decrypts wrongly or partBase is not the id")
	}

	if _, err := f.megaResolve(context.Background(), "Missing1", linkKey); err != ErrMegaRefused {
		t.Fatalf("refusal = %v", err)
	}
	if _, _, ok := megaLink(&url.URL{Host: "mega.nz", Path: "/folder/AbCd_123", Fragment: k}); ok {
		t.Fatal("a folder link is not a file link")
	}
}
