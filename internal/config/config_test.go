// Copyright (C) 2026 glowinthedark
//
// SPDX-License-Identifier: GPL-3.0-or-later

package config

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/wuweidict/wudict/internal/basedir"
)

func TestLayering(t *testing.T) {
	dir := t.TempDir()
	toml := filepath.Join(dir, Name)
	os.WriteFile(toml, []byte(`
# comment
DICT_DIR = "/from/toml"
SERVER_PORT = "9999"
NO_BROWSER = "1"
`), 0o644)

	t.Setenv("SERVER_PORT", "7777") // env beats toml
	cfg, err := Load(toml, map[string]string{"SERVER_IP": "0.0.0.0"})
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.DictDirs) != 1 || cfg.DictDirs[0] != "/from/toml" {
		t.Errorf("toml DICT_DIR: %q", cfg.DictDirs)
	}
	if cfg.Port != "7777" {
		t.Errorf("env should beat toml: %q", cfg.Port)
	}
	if cfg.IP != "0.0.0.0" {
		t.Errorf("flag should win: %q", cfg.IP)
	}
	if !cfg.NoBrowser {
		t.Error("NO_BROWSER not applied")
	}
	if cfg.Addr() != "0.0.0.0:7777" {
		t.Errorf("Addr: %q", cfg.Addr())
	}
}

func TestDefaultsAndMissingFile(t *testing.T) {
	isolate(t) // no real ~/.wudict/wudict.toml may leak into the defaults
	t.Setenv("SERVER_PORT", "")
	t.Setenv("DICT_DIR", "")
	cfg, err := Load("", nil) // no explicit file; defaults apply
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Port != "6888" || cfg.IP != "127.0.0.1" {
		t.Errorf("defaults: %+v", cfg)
	}
	if _, err := Load(filepath.Join(t.TempDir(), "nope.toml"), nil); err == nil {
		t.Error("explicit missing config must error")
	}
}

func TestSaveKey(t *testing.T) {
	p := filepath.Join(t.TempDir(), Name)
	os.WriteFile(p, []byte("# header\n# DICT_DIR    = \"~/Dictionaries\"      # comment\n# SERVER_PORT = \"6888\"\n"), 0o644)
	if err := SaveKey(p, "DICT_DIR", "/data/dicts"); err != nil {
		t.Fatal(err)
	}
	if err := SaveKey(p, "NEW_KEY", "v"); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(p)
	s := string(data)
	if !strings.Contains(s, "DICT_DIR = \"/data/dicts\"") {
		t.Errorf("commented key not uncommented: %q", s)
	}
	if strings.Contains(s, "# DICT_DIR") {
		t.Errorf("old commented line kept: %q", s)
	}
	if !strings.Contains(s, "# header") || !strings.Contains(s, "# SERVER_PORT") {
		t.Errorf("unrelated lines lost: %q", s)
	}
	if !strings.Contains(s, "NEW_KEY = \"v\"") {
		t.Errorf("append failed: %q", s)
	}
	// round-trip: parse sees the saved value
	r, err := loadFile(p)
	if err != nil || r.path != p || r.vals["DICT_DIR"] != "/data/dicts" {
		t.Errorf("round-trip: %v %q %v", r.vals, r.path, err)
	}
}

func TestExpandHome(t *testing.T) {
	home, _ := os.UserHomeDir()
	if got := ExpandHome("~/x"); got != filepath.Join(home, "x") {
		t.Errorf("ExpandHome: %q", got)
	}
	if got := ExpandHome("/abs"); got != "/abs" {
		t.Errorf("abs untouched: %q", got)
	}
	if got := ExpandHome("~"); got != home {
		t.Errorf("bare ~: %q", got)
	}
	if got := ExpandHome("~user/x"); got != "~user/x" {
		t.Errorf("~user untouched: %q", got)
	}
	// "~\x" is a home-relative path only where the backslash separates
	want := `~\x`
	if filepath.Separator == '\\' {
		want = filepath.Join(home, "x")
	}
	if got := ExpandHome(`~\x`); got != want {
		t.Errorf("ExpandHome(~\\x) = %q, want %q", got, want)
	}
}

// DICT_DIR accepts one folder or several, in every layer's own spelling.
func TestDictDirList(t *testing.T) {
	sep := string(os.PathListSeparator)
	home, _ := os.UserHomeDir()
	cases := []struct {
		name, value string
		want        []string
	}{
		{"single", "/a", []string{"/a"}},
		{"toml array", `["/a", "/b"]`, []string{"/a", "/b"}},
		{"toml array, single entry", `["/a"]`, []string{"/a"}},
		{"env separated", "/a" + sep + "/b", []string{"/a", "/b"}},
		{"blanks dropped", "/a" + sep + sep + " /b ", []string{"/a", "/b"}},
		{"tilde expanded", "~/d", []string{filepath.Join(home, "d")}},
		{"order preserved", `["/b","/a"]`, []string{"/b", "/a"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := ParseList(c.value)
			if len(got) != len(c.want) {
				t.Fatalf("ParseList(%q) = %q, want %q", c.value, got, c.want)
			}
			for i := range got {
				if got[i] != c.want[i] {
					t.Fatalf("ParseList(%q) = %q, want %q", c.value, got, c.want)
				}
			}
		})
	}
	// and it round-trips through the config file in both shapes
	dir := t.TempDir()
	for _, dirs := range [][]string{{"/only"}, {"/a", "/b b"}} {
		p := filepath.Join(dir, Name)
		if err := os.WriteFile(p, []byte("# DICT_DIR = \"~/Dictionaries\"\nSERVER_PORT = \"1\"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := SaveKeyRaw(p, "DICT_DIR", FormatList(dirs)); err != nil {
			t.Fatal(err)
		}
		cfg, err := Load(p, nil)
		if err != nil {
			t.Fatal(err)
		}
		if len(cfg.DictDirs) != len(dirs) {
			t.Fatalf("round-trip %q → %q", dirs, cfg.DictDirs)
		}
		for i := range dirs {
			if cfg.DictDirs[i] != dirs[i] {
				t.Fatalf("round-trip %q → %q", dirs, cfg.DictDirs)
			}
		}
		if cfg.Port != "1" {
			t.Errorf("saving DICT_DIR disturbed another key: %q", cfg.Port)
		}
	}
}

// BROWSER_EXTENSIONS holds origins, not paths: every value contains the ':'
// that ParseList splits on, so it needs its own parser (D69).
func TestParseOrigins(t *testing.T) {
	const chrome = "chrome-extension://abcdefghijklmnopabcdefghijklmnop"
	const fx = "moz-extension://5b1f2e3c-0a4d-4c7e-9f21-2a6c8d1e7b40"
	cases := []struct {
		name, value string
		want        []string
	}{
		{"single", chrome, []string{chrome}},
		{"toml array", `["` + chrome + `", "` + fx + `"]`, []string{chrome, fx}},
		{"comma separated", chrome + "," + fx, []string{chrome, fx}},
		{"space separated", chrome + " " + fx, []string{chrome, fx}},
		{"the scheme's colon is not a separator", chrome, []string{chrome}},
		{"trailing slash dropped", chrome + "/", []string{chrome}},
		{"blanks dropped", " , " + chrome + " , ", []string{chrome}},
		{"empty", "", nil},
		{"empty array", "[]", nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := ParseOrigins(c.value)
			if len(got) != len(c.want) {
				t.Fatalf("ParseOrigins(%q) = %q, want %q", c.value, got, c.want)
			}
			for i := range got {
				if got[i] != c.want[i] {
					t.Fatalf("ParseOrigins(%q) = %q, want %q", c.value, got, c.want)
				}
			}
		})
	}
	// …and it survives the config file, whose parser strips quotes and a
	// trailing " #" comment before this ever sees the value.
	p := filepath.Join(t.TempDir(), Name)
	if err := os.WriteFile(p, []byte("BROWSER_EXTENSIONS = [\""+chrome+"\"]\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(p, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.BrowserExtensions) != 1 || cfg.BrowserExtensions[0] != chrome {
		t.Errorf("BROWSER_EXTENSIONS from file = %q, want [%q]", cfg.BrowserExtensions, chrome)
	}
	blank := filepath.Join(t.TempDir(), Name)
	if err := os.WriteFile(blank, []byte("SERVER_PORT = \"1\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if cfg2, _ := Load(blank, nil); len(cfg2.BrowserExtensions) != 0 {
		t.Errorf("unset BROWSER_EXTENSIONS = %q, want empty (any extension)", cfg2.BrowserExtensions)
	}
}

// WEB_ORIGINS is the same list shape, read by the same parser, with the
// opposite default: unset means no web page may read the API (D69).
func TestWebOriginsConfig(t *testing.T) {
	const local = "http://localhost:3000"
	const notes = "https://notes.example.com"

	p := filepath.Join(t.TempDir(), Name)
	if err := os.WriteFile(p, []byte("WEB_ORIGINS = [\""+local+"\", \""+notes+"\"]\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(p, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.WebOrigins) != 2 || cfg.WebOrigins[0] != local || cfg.WebOrigins[1] != notes {
		t.Errorf("WEB_ORIGINS from file = %q, want [%q %q]", cfg.WebOrigins, local, notes)
	}
	// The environment outranks the file, and separates with commas rather than
	// os.PathListSeparator - which is the ':' inside every one of these values.
	t.Setenv("WEB_ORIGINS", local+","+notes)
	if cfg, err = Load(p, nil); err != nil {
		t.Fatal(err)
	}
	if len(cfg.WebOrigins) != 2 || cfg.WebOrigins[0] != local {
		t.Errorf("WEB_ORIGINS from env = %q, want two origins starting %q", cfg.WebOrigins, local)
	}
	if cfg.Origin("WEB_ORIGINS") != OriginEnv {
		t.Errorf("WEB_ORIGINS origin = %q, want %q", cfg.Origin("WEB_ORIGINS"), OriginEnv)
	}

	blank := filepath.Join(t.TempDir(), Name)
	if err := os.WriteFile(blank, []byte("SERVER_PORT = \"1\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("WEB_ORIGINS", "")
	if cfg2, _ := Load(blank, nil); len(cfg2.WebOrigins) != 0 {
		t.Errorf("unset WEB_ORIGINS = %q, want empty (no web page)", cfg2.WebOrigins)
	}
}

// AUTO_INDEX is on|off, and an existing wudict.toml saying "fuzzy" (the
// retired search mode it was named after) must keep working.
func TestAutoIndexValues(t *testing.T) {
	for _, c := range []struct {
		in   string
		want string
		on   bool
	}{
		{"", AutoIndexOn, true}, // unset → default
		{"on", AutoIndexOn, true},
		{"ON", AutoIndexOn, true},
		{"1", AutoIndexOn, true},
		{"true", AutoIndexOn, true},
		{"fuzzy", AutoIndexOn, true}, // the legacy spelling
		{"off", AutoIndexOff, false},
		{"OFF", AutoIndexOff, false},
		{"0", AutoIndexOff, false},
		{"false", AutoIndexOff, false},
		{"no", AutoIndexOff, false},
	} {
		dir := t.TempDir()
		p := filepath.Join(dir, Name)
		body := ""
		if c.in != "" {
			body = "AUTO_INDEX = \"" + c.in + "\"\n"
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
		cfg, err := Load(p, nil)
		if err != nil {
			t.Fatal(err)
		}
		if cfg.AutoIndex != c.want || cfg.AutoIndexEnabled() != c.on {
			t.Errorf("AUTO_INDEX=%q → %q (enabled=%v), want %q (%v)",
				c.in, cfg.AutoIndex, cfg.AutoIndexEnabled(), c.want, c.on)
		}
	}
}

// IMPORT_KEEP decides the fate of an imported archive. Anything unrecognised
// must land on "ask": the two mistakes are not symmetric, and a typo that
// resolved to "delete" would remove a file the user may have no other copy of.
func TestParseHostList(t *testing.T) {
	for _, c := range []struct {
		in   string
		want []string
	}{
		{"", nil},
		{"example.org", []string{"example.org"}},
		{"Example.org, *.cloud.example.net , .dl.example.com", []string{"example.org", "cloud.example.net", "dl.example.com"}},
		{"https://example.org/dicts/, example.net:8443", []string{"example.org", "example.net"}},
		// wudict.toml spells lists as arrays; the raw text arrives here.
		{`["example.org", "cloud.example.net"]`, []string{"example.org", "cloud.example.net"}},
		{"[\n  'example.org',\n  \"cloud.example.net\",\n]", []string{"example.org", "cloud.example.net"}},
		{"[]", nil},
	} {
		got := ParseHostList(c.in)
		if strings.Join(got, "|") != strings.Join(c.want, "|") {
			t.Errorf("ParseHostList(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// IMPORT_URL_HOSTS in its two wudict.toml spellings, and from the environment.
func TestImportURLHostsConfig(t *testing.T) {
	want := []string{"example.org", "cloud.example.net"}
	for _, body := range []string{
		"IMPORT_URL_HOSTS = [\"example.org\", \"cloud.example.net\"]\n",
		"IMPORT_URL_HOSTS = \"example.org, cloud.example.net\"\n",
	} {
		p := filepath.Join(t.TempDir(), Name)
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
		cfg, err := Load(p, nil)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Join(cfg.ImportURLHosts, "|") != strings.Join(want, "|") {
			t.Errorf("%s→ %q, want %q", body, cfg.ImportURLHosts, want)
		}
	}
	p := filepath.Join(t.TempDir(), Name)
	if err := os.WriteFile(p, []byte("SERVER_PORT = \"1\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("IMPORT_URL_HOSTS", "example.org,cloud.example.net")
	cfg, err := Load(p, nil)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(cfg.ImportURLHosts, "|") != strings.Join(want, "|") {
		t.Errorf("env → %q, want %q", cfg.ImportURLHosts, want)
	}
}

func TestImportKeepValues(t *testing.T) {
	for _, c := range []struct{ in, want string }{
		{"", ImportKeepAsk}, // unset → default
		{"ask", ImportKeepAsk},
		{"ASK", ImportKeepAsk},
		{"keep", ImportKeepYes},
		{"yes", ImportKeepYes},
		{"1", ImportKeepYes},
		{"delete", ImportKeepDelete},
		{"remove", ImportKeepDelete},
		{"0", ImportKeepDelete},
		{"nonsense", ImportKeepAsk},
	} {
		dir := t.TempDir()
		p := filepath.Join(dir, Name)
		body := ""
		if c.in != "" {
			body = "IMPORT_KEEP = \"" + c.in + "\"\n"
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
		cfg, err := Load(p, nil)
		if err != nil {
			t.Fatal(err)
		}
		if cfg.ImportKeep != c.want {
			t.Errorf("IMPORT_KEEP=%q → %q, want %q", c.in, cfg.ImportKeep, c.want)
		}
	}
}

func TestParseWorkersAndSize(t *testing.T) {
	cpu := runtime.NumCPU()
	for in, want := range map[string]int{
		"1": 1, "2": 2, "auto": cpu, "all": cpu, "max": cpu, "0": cpu, "-1": cpu,
		"": 1, "nonsense": 1, "99999": cpu,
	} {
		if got := ParseWorkers(in); got != want {
			t.Errorf("ParseWorkers(%q) = %d, want %d", in, got, want)
		}
	}
	for in, want := range map[string]int64{
		"4GB": 4 << 30, "512M": 512 << 20, "1500MB": 1500 << 20, "2048": 2048,
		"1.5GB": int64(1.5 * float64(int64(1)<<30)), "0": 0, "off": 0, "": 0, "junk": 0,
	} {
		if got := ParseSize(in); got != want {
			t.Errorf("ParseSize(%q) = %d, want %d", in, got, want)
		}
	}
	// default is one dictionary at a time: background work must not take the machine
	cfg, err := Load(filepath.Join(t.TempDir(), "none.toml"), nil)
	if err == nil && cfg.IndexWorkers != 1 {
		t.Errorf("default INDEX_WORKERS = %d, want 1", cfg.IndexWorkers)
	}
}

// isolate points the three search anchors at empty temporary directories, so a
// test sees only the files it creates - never the developer's real ~/.wudict
// or /etc. Returns the executable and home directories, in search order.
func isolate(t *testing.T) (exe, home string) {
	t.Helper()
	root := t.TempDir()
	exe, home, etc := filepath.Join(root, "bin"), filepath.Join(root, "home"), filepath.Join(root, "etc")
	for _, d := range []string{exe, home, etc} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	oldExe, oldHome, oldSys := exeDir, homeDir, systemDir
	exeDir = func() string { return exe }
	homeDir = func() string { return home }
	systemDir = etc
	t.Cleanup(func() { exeDir, homeDir, systemDir = oldExe, oldHome, oldSys })
	// a Linux runner's own XDG folders must not leak in either
	for _, k := range []string{"XDG_CONFIG_HOME", "XDG_DATA_HOME", "XDG_STATE_HOME", "XDG_CACHE_HOME"} {
		t.Setenv(k, "")
	}
	t.Setenv("XDG_CONFIG_DIRS", filepath.Join(root, "xdg"))
	// the ~/.wudict layout unless a test asks for another (setGOOS)
	setGOOS(t, "darwin")
	return exe, home
}

// Where the config file is looked for, which one wins, and - the point of D32 -
// where a new one is written.
func TestConfigLocation(t *testing.T) {
	write := func(t *testing.T, path, body string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	t.Run("created in the home directory, never beside the executable", func(t *testing.T) {
		exe, home := isolate(t)
		p, created, err := EnsureConfigFile()
		if err != nil || !created {
			t.Fatalf("EnsureConfigFile() = %q, %v, %v", p, created, err)
		}
		if want := filepath.Join(home, ".wudict", Name); p != want {
			t.Errorf("created %q, want %q", p, want)
		}
		// the executable's directory belongs to whoever installed us
		if _, err := os.Stat(filepath.Join(exe, Name)); !os.IsNotExist(err) {
			t.Errorf("wrote next to the executable: %v", err)
		}
		// idempotent: the second run finds the first one
		p2, created2, err := EnsureConfigFile()
		if err != nil || created2 || p2 != p {
			t.Errorf("second call = %q, %v, %v; want %q, false", p2, created2, err, p)
		}
	})

	t.Run("an existing file is never overwritten", func(t *testing.T) {
		exe, _ := isolate(t)
		portable := filepath.Join(exe, Name)
		write(t, portable, `SERVER_PORT = "1234"`)
		p, created, err := EnsureConfigFile()
		if err != nil || created || p != portable {
			t.Fatalf("EnsureConfigFile() = %q, %v, %v; want %q, false", p, created, err, portable)
		}
	})

	t.Run("portable beats home, and says so", func(t *testing.T) {
		exe, home := isolate(t)
		write(t, filepath.Join(exe, Name), `SERVER_PORT = "1111"`)
		write(t, filepath.Join(home, ".wudict", Name), `SERVER_PORT = "2222"`)
		cfg, err := Load("", nil)
		if err != nil {
			t.Fatal(err)
		}
		if cfg.Port != "1111" {
			t.Errorf("port = %q, want the portable file's 1111", cfg.Port)
		}
		if !cfg.Portable {
			t.Error("Portable not reported for a file beside the executable")
		}
		if len(cfg.Shadowed) != 1 || cfg.Shadowed[0] != filepath.Join(home, ".wudict", Name) {
			t.Errorf("Shadowed = %q, want the home file", cfg.Shadowed)
		}
	})

	t.Run("home beats system and is not portable", func(t *testing.T) {
		_, home := isolate(t)
		write(t, filepath.Join(home, ".wudict", Name), `SERVER_PORT = "2222"`)
		write(t, filepath.Join(systemDir, Name), `SERVER_PORT = "3333"`)
		cfg, err := Load("", nil)
		if err != nil {
			t.Fatal(err)
		}
		if cfg.Port != "2222" || cfg.Portable {
			t.Errorf("port = %q portable = %v, want 2222 and not portable", cfg.Port, cfg.Portable)
		}
		if len(cfg.Shadowed) != 1 {
			t.Errorf("Shadowed = %q, want the system file", cfg.Shadowed)
		}
	})

	t.Run("the working directory is not searched", func(t *testing.T) {
		isolate(t)
		dir := t.TempDir()
		write(t, filepath.Join(dir, Name), `SERVER_PORT = "9999"`)
		write(t, filepath.Join(dir, "config.toml"), `SERVER_PORT = "9999"`)
		wd, err := os.Getwd()
		if err != nil {
			t.Fatal(err)
		}
		if err := os.Chdir(dir); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { os.Chdir(wd) })
		cfg, err := Load("", nil)
		if err != nil {
			t.Fatal(err)
		}
		if cfg.Source != "" || cfg.Port == "9999" {
			t.Errorf("picked up a file from the working directory: %q", cfg.Source)
		}
	})

	t.Run("an explicit path must exist", func(t *testing.T) {
		exe, _ := isolate(t)
		write(t, filepath.Join(exe, Name), `SERVER_PORT = "1111"`)
		// silently falling back to the portable file would hide the typo
		if _, err := Load(filepath.Join(t.TempDir(), "typo.toml"), nil); err == nil {
			t.Error("explicit missing config must error")
		}
	})

	// On a free Unix desktop the file is created in $XDG_CONFIG_HOME/wudict,
	// names the XDG folders it defaults to, and $XDG_CONFIG_DIRS is searched
	// before /etc/wudict.
	t.Run("XDG: created in the config folder", func(t *testing.T) {
		_, home := isolate(t)
		setGOOS(t, "linux")
		p, created, err := EnsureConfigFile()
		if err != nil || !created {
			t.Fatalf("EnsureConfigFile() = %q, %v, %v", p, created, err)
		}
		if want := filepath.Join(home, ".config", "wudict", Name); p != want {
			t.Errorf("created %q, want %q", p, want)
		}
		body, _ := os.ReadFile(p)
		for _, want := range []string{"~/.config/wudict/wudict.toml", `"~/.local/share/wudict/db"`, `"~/.local/share/wudict/lemmas"`, "~/.local/state/wudict/token"} {
			if !strings.Contains(string(body), want) {
				t.Errorf("the template does not name %s", want)
			}
		}
		if strings.Contains(string(body), ".wudict/") {
			t.Error("the template still names ~/.wudict")
		}
		if want := filepath.Join(home, ".local", "share", "wudict", "lemmas"); defaults().LemmaDir != want {
			t.Errorf("LEMMA_DIR default = %q, want %q", defaults().LemmaDir, want)
		}
		if want := filepath.Join(home, ".local", "state", "wudict", "token"); TokenPath() != want {
			t.Errorf("TokenPath = %q, want %q", TokenPath(), want)
		}
	})

	t.Run("XDG: an unmoved ~/.wudict stays in effect", func(t *testing.T) {
		_, home := isolate(t)
		setGOOS(t, "linux")
		old := filepath.Join(home, ".wudict", Name)
		write(t, old, `SERVER_PORT = "2222"`)
		cfg, err := Load("", nil)
		if err != nil {
			t.Fatal(err)
		}
		if cfg.Source != old || cfg.Port != "2222" {
			t.Errorf("Source = %q port %q, want %q and 2222", cfg.Source, cfg.Port, old)
		}
	})

	t.Run("XDG: config dirs before /etc/wudict", func(t *testing.T) {
		isolate(t)
		setGOOS(t, "linux")
		xdg := filepath.Join(t.TempDir(), "xdg")
		t.Setenv("XDG_CONFIG_DIRS", xdg)
		write(t, filepath.Join(xdg, "wudict", Name), `SERVER_PORT = "4444"`)
		write(t, filepath.Join(systemDir, Name), `SERVER_PORT = "3333"`)
		cfg, err := Load("", nil)
		if err != nil {
			t.Fatal(err)
		}
		if cfg.Port != "4444" || len(cfg.Shadowed) != 1 {
			t.Errorf("port = %q, Shadowed = %q; want 4444 and /etc/wudict shadowed", cfg.Port, cfg.Shadowed)
		}
	})

	t.Run("not XDG: the template is the ~/.wudict one", func(t *testing.T) {
		isolate(t)
		setGOOS(t, "darwin")
		p, _, err := EnsureConfigFile()
		if err != nil {
			t.Fatal(err)
		}
		if body, _ := os.ReadFile(p); string(body) != configTemplate {
			t.Error("the template changed on a platform that keeps ~/.wudict")
		}
	})
}

func setGOOS(t *testing.T, g string) {
	t.Helper()
	// The XDG layout is a Unix one: POSIX paths, ":" between config dirs.
	// Simulated on Windows, a drive letter breaks both, and wudict never
	// resolves it there.
	if basedir.XDG(g) && runtime.GOOS == "windows" {
		t.Skip("the XDG layout is not simulated on Windows")
	}
	old := goos
	goos = g
	t.Cleanup(func() { goos = old })
}

// COMPRESSION and the key it replaced, NO_COMPRESS (D191): every spelling, the
// layer that wins, and that nothing invalid is fatal.
func TestCompressionResolve(t *testing.T) {
	zstd1 := Compression{Codec: CodecZstd, Level: 1}
	none := Compression{Codec: CodecNone}
	for _, tc := range []struct {
		name     string
		file     string            // wudict.toml body
		env      map[string]string // environment
		flags    map[string]string
		want     Compression
		origin   string
		problem  string // substring of a Problem expected; "" = none
		problems int    // how many Problems; 0 means 1 when problem is set
	}{
		{name: "unset", want: zstd1, origin: OriginDefault},
		{name: "zstd", env: map[string]string{"COMPRESSION": "zstd"}, want: zstd1, origin: OriginEnv},
		{name: "case and space", env: map[string]string{"COMPRESSION": " ZSTD-3 "}, want: Compression{CodecZstd, 3}, origin: OriginEnv},
		{name: "zstd tier 2", env: map[string]string{"COMPRESSION": "zstd-2"}, want: zstd1, origin: OriginEnv},
		{name: "zstd tier 5", env: map[string]string{"COMPRESSION": "zstd-5"}, want: Compression{CodecZstd, 3}, origin: OriginEnv},
		{name: "zstd tier 9", env: map[string]string{"COMPRESSION": "zstd-9"}, want: Compression{CodecZstd, 6}, origin: OriginEnv},
		{name: "zstd tier 19", env: map[string]string{"COMPRESSION": "zstd-19"}, want: Compression{CodecZstd, 10}, origin: OriginEnv},
		{name: "deflate bare", env: map[string]string{"COMPRESSION": "deflate"}, want: Compression{CodecDeflate, deflateLevel()}, origin: OriginEnv},
		{name: "deflate-8", file: `COMPRESSION = "deflate-8"`, want: Compression{CodecDeflate, 8}, origin: OriginFile},
		{name: "none", flags: map[string]string{"COMPRESSION": "none"}, want: none, origin: OriginFlag},
		{name: "none with a level", env: map[string]string{"COMPRESSION": "none-3"}, want: zstd1, origin: OriginDefault, problem: "is not a compression"},
		{name: "deflate-0", env: map[string]string{"COMPRESSION": "deflate-0"}, want: zstd1, origin: OriginDefault, problem: "is not a compression"},
		{name: "deflate-10", env: map[string]string{"COMPRESSION": "deflate-10"}, want: zstd1, origin: OriginDefault, problem: "is not a compression"},
		{name: "zstd-23", env: map[string]string{"COMPRESSION": "zstd-23"}, want: zstd1, origin: OriginDefault, problem: "is not a compression"},
		{name: "typo", env: map[string]string{"COMPRESSION": "zst"}, want: zstd1, origin: OriginDefault, problem: "is not a compression"},
		{name: "old small", file: `COMPRESSION = "small"`, want: zstd1, origin: OriginFile, problem: "earlier spelling"},
		{name: "old fast", env: map[string]string{"COMPRESSION": "fast"}, want: zstd1, origin: OriginEnv, problem: "earlier spelling"},
		{name: "NO_COMPRESS=1", env: map[string]string{"NO_COMPRESS": "1"}, want: none, origin: OriginEnv, problem: "none applies"},
		{name: "NO_COMPRESS=yes in file", file: `NO_COMPRESS = "yes"`, want: none, origin: OriginFile, problem: "none applies"},
		{name: "NO_COMPRESS=0", env: map[string]string{"NO_COMPRESS": "0"}, want: zstd1, origin: OriginDefault, problem: "has no effect. Delete it"},
		{name: "NO_COMPRESS=off", env: map[string]string{"NO_COMPRESS": "off"}, want: zstd1, origin: OriginDefault, problem: "has no effect. Delete it"},
		{name: "--no-compress", flags: map[string]string{"NO_COMPRESS": "1"}, want: none, origin: OriginFlag, problem: "none applies"},
		// one layer: a codec beats NO_COMPRESS, which beats fast/small
		{name: "same layer codec wins", env: map[string]string{"COMPRESSION": "deflate", "NO_COMPRESS": "1"},
			want: Compression{CodecDeflate, deflateLevel()}, origin: OriginEnv, problem: "has no effect: COMPRESSION"},
		{name: "same layer beats small", file: "COMPRESSION = \"small\"\nNO_COMPRESS = \"1\"", want: none, origin: OriginFile, problem: "none applies", problems: 2},
		// a level spelling never beats NO_COMPRESS, whatever the layers
		{name: "file NO_COMPRESS over env small", file: `NO_COMPRESS = "1"`, env: map[string]string{"COMPRESSION": "small"},
			want: none, origin: OriginFile, problem: "none applies", problems: 2},
		{name: "file NO_COMPRESS over flag fast", file: `NO_COMPRESS = "1"`, flags: map[string]string{"COMPRESSION": "fast"},
			want: none, origin: OriginFile, problem: "none applies", problems: 2},
		// different layers: the higher one wins
		{name: "env NO_COMPRESS over file codec", file: `COMPRESSION = "deflate"`, env: map[string]string{"NO_COMPRESS": "1"},
			want: none, origin: OriginEnv, problem: "none applies"},
		{name: "env codec over file NO_COMPRESS", file: `NO_COMPRESS = "1"`, env: map[string]string{"COMPRESSION": "zstd-3"},
			want: Compression{CodecZstd, 3}, origin: OriginEnv, problem: "has no effect: COMPRESSION"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			isolate(t)
			for _, k := range []string{"COMPRESSION", "NO_COMPRESS"} {
				t.Setenv(k, tc.env[k])
			}
			toml := filepath.Join(t.TempDir(), Name)
			os.WriteFile(toml, []byte(tc.file+"\n"), 0o644)
			cfg, err := Load(toml, tc.flags)
			if err != nil {
				t.Fatal(err)
			}
			if cfg.Compression != tc.want {
				t.Errorf("Compression = %v, want %v", cfg.Compression, tc.want)
			}
			if o := cfg.Origin("COMPRESSION"); o != tc.origin {
				t.Errorf("origin = %q, want %q", o, tc.origin)
			}
			var got []string
			for _, p := range cfg.Problems {
				if strings.Contains(p, "COMPRESS") {
					got = append(got, p)
				}
			}
			want := tc.problems
			if want == 0 && tc.problem != "" {
				want = 1
			}
			found := tc.problem == ""
			for _, p := range got {
				found = found || strings.Contains(p, tc.problem)
			}
			if len(got) != want || !found {
				t.Errorf("problems %q, want %d, one containing %q", got, want, tc.problem)
			}
		})
	}
}

// What Effective reports for COMPRESSION must parse back to the same value.
func TestCompressionRoundTrip(t *testing.T) {
	for _, v := range []string{"zstd", "zstd-1", "zstd-3", "zstd-6", "zstd-10", "deflate", "deflate-1", "deflate-9", "none"} {
		c, ok := ParseCompression(v)
		if !ok {
			t.Fatalf("%q does not parse", v)
		}
		back, ok := ParseCompression(c.String())
		if !ok || back != c {
			t.Errorf("%q -> %v -> %v, %v", v, c, back, ok)
		}
	}
}

func TestEffective(t *testing.T) {
	dir := t.TempDir()
	toml := filepath.Join(dir, Name)
	os.WriteFile(toml, []byte("INDEX_WORKERS = \"1\"\nPREVIEW_MEMORY = \"2GB\"\n"), 0o644)
	t.Setenv("COMPRESSION", "deflate-8")
	cfg, err := Load(toml, map[string]string{"SERVER_PORT": "7000"})
	if err != nil {
		t.Fatal(err)
	}
	eff := cfg.Effective()
	if len(eff) != len(Tunables) {
		t.Fatalf("Effective() has %d keys, want %d", len(eff), len(Tunables))
	}
	for _, tc := range []struct{ key, value, origin string }{
		{"COMPRESSION", "deflate-8", OriginEnv},
		{"PREVIEW_MEMORY", "2GB", OriginFile},
		{"INDEX_WORKERS", "1", OriginFile},
		{"SERVER_PORT", "7000", OriginFlag},
		{"SERVER_IP", cfg.IP, OriginDefault},
	} {
		got := eff[tc.key]
		if got.Value != tc.value || got.Origin != tc.origin {
			t.Errorf("Effective()[%q] = %+v, want {%q %q}", tc.key, got, tc.value, tc.origin)
		}
	}
	// The values must read back the way the config would accept them.
	if v := eff["SEARCH_MEMORY"].Value; ParseSize(v) != cfg.SearchMemory {
		t.Errorf("SEARCH_MEMORY %q reparses to %d, want %d", v, ParseSize(v), cfg.SearchMemory)
	}
}

func TestFormatSize(t *testing.T) {
	for _, tc := range []struct {
		n    int64
		want string
	}{
		{0, "0"}, {-1, "0"}, {1 << 30, "1GB"}, {3 << 30, "3GB"},
		{64 << 20, "64MB"}, {1536 << 20, "1536MB"}, {1 << 10, "1KB"}, {1000, "1000"},
	} {
		if got := FormatSize(tc.n); got != tc.want {
			t.Errorf("FormatSize(%d) = %q, want %q", tc.n, got, tc.want)
		}
	}
}

// An IPv6 literal has to be bracketed in an address. Plain concatenation
// produced ":::6888" for "::1", which net.Listen rejects, so SERVER_IP=::1
// could not be bound at all.
func TestAddrBracketsIPv6(t *testing.T) {
	cases := []struct{ ip, port, want string }{
		{"::1", "6888", "[::1]:6888"},
		{"fe80::1%en0", "80", "[fe80::1%en0]:80"},
		{"127.0.0.1", "6888", "127.0.0.1:6888"},
		{"0.0.0.0", "6888", "0.0.0.0:6888"},
		{"", "6888", ":6888"},
	}
	for _, c := range cases {
		cfg := Config{IP: c.ip, Port: c.port}
		if got := cfg.Addr(); got != c.want {
			t.Errorf("Addr(%q, %q) = %q, want %q", c.ip, c.port, got, c.want)
		}
	}
}

// Every boolean key reads through isOff; this asserts every key against every
// spelling a person actually writes, so a key added with an inline `v != "0"`
// (which reads "no"/"off" as YES) fails here rather than in a bug report.
func TestBooleanKeysAgree(t *testing.T) {
	keys := map[string]func(Config) bool{
		"NO_BROWSER":          func(c Config) bool { return c.NoBrowser },
		"VERBOSE":             func(c Config) bool { return c.Verbose },
		"USE_CACHED":          func(c Config) bool { return c.UseCached },
		"ALLOW_REMOTE_DELETE": func(c Config) bool { return c.AllowRemoteDelete },
		"TRAY":                func(c Config) bool { return c.Tray != nil && *c.Tray },
	}
	values := map[string]bool{
		"0": false, "false": false, "FALSE": false, "off": false,
		"no": false, "n": false, " no ": false,
		"1": true, "true": true, "yes": true, "on": true,
	}
	for key, read := range keys {
		for v, want := range values {
			t.Run(key+"="+v, func(t *testing.T) {
				isolate(t)
				t.Setenv(key, v)
				cfg, err := Load("", nil)
				if err != nil {
					t.Fatal(err)
				}
				if got := read(cfg); got != want {
					t.Errorf("%s=%q gave %v, want %v", key, v, got, want)
				}
			})
		}
	}
}

// The unset case is separate because it is a different question: nothing was
// said, so the built-in default stands. TRAY's default is the tri-state nil
// ("decide from how the app was launched", D74), not false.
func TestBooleanKeysUnset(t *testing.T) {
	isolate(t)
	for _, k := range []string{"NO_BROWSER", "VERBOSE", "USE_CACHED", "ALLOW_REMOTE_DELETE", "TRAY"} {
		t.Setenv(k, "")
	}
	cfg, err := Load("", nil)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.NoBrowser || cfg.Verbose || cfg.UseCached || cfg.AllowRemoteDelete {
		t.Errorf("an unset boolean turned something on: %+v", cfg)
	}
	if cfg.Tray != nil {
		t.Errorf("TRAY unset must stay tri-state nil, got %v", *cfg.Tray)
	}
}
