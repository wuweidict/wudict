package server

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/wuweidict/wudict/internal/logx"
)

// StateFile holds the part of the UI that describes the COLLECTION rather than
// the browser looking at it: which dictionaries are searched, and in what
// order. Everything that describes the browser instead — theme, wide mode, the
// last dictionary picked in the dropdown — stays in localStorage, where it
// belongs.
//
// The split is not cosmetic. localStorage is keyed by origin, so
// scheme://host:port is part of the identity: changing SERVER_PORT, reaching
// the same server as 127.0.0.1 instead of localhost, or opening it from a
// phone each hands the user a blank slate. Curating a hundred dictionaries and
// losing it to a port change is not a preference that survived, it is a
// preference that was never stored. The server, which owns the dictionaries,
// owns the answer to "which of them do I search".
//
// It lives beside the wudict.toml in effect rather than at a hardcoded path,
// so a portable install (D32) keeps its state on the same stick as its config,
// and --config points both at once. It is deliberately NOT inside the library
// folders: D20 defines those as individually transferable units, and "which
// dictionaries are enabled" is a fact about the set, not about any member.
const StateFile = "state.json"

// prefsVersion is written to the file so a future format change can be
// recognised rather than guessed at.
const prefsVersion = 1

// DictPref is one dictionary's remembered state. The path is what makes this
// survivable: ids are sha256(path)[:12] (see pathID), so moving a dictionary
// folder changes every id at once. Recording the path — and, through it, the
// file name — lets the state be re-attached instead of silently reset.
type DictPref struct {
	ID   string `json:"id"`
	Path string `json:"path"`
	Name string `json:"name,omitempty"` // display name, for a readable file
	Off  bool   `json:"off,omitempty"`  // excluded from "All dictionaries"
}

type prefsFile struct {
	Version int        `json:"version"`
	Dicts   []DictPref `json:"dicts"` // array ORDER is the user's order
}

// Prefs is the state file, loaded once and written on change. A Prefs with an
// empty path is in-memory only: it answers questions and forgets on exit,
// which is what tests and a home-less environment need.
type Prefs struct {
	path   string
	mu     sync.RWMutex
	exists bool // a file was there when we started: no state to adopt otherwise
	dicts  []DictPref
}

// LoadPrefs reads the state file. It never fails: a missing file is the normal
// first run, and an unreadable or corrupt one must not stop the app from
// serving dictionaries — the worst case is that the user re-curates a list.
func LoadPrefs(path string) *Prefs {
	p := &Prefs{path: path}
	if path == "" {
		return p
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if !os.IsNotExist(err) {
			logx.Warn("could not read %s: %v", path, err)
		}
		return p
	}
	var f prefsFile
	if err := json.Unmarshal(data, &f); err != nil {
		// Loud, because the next save overwrites it: the user gets one chance
		// to notice their hand-edit was rejected.
		logx.Warn("ignoring malformed %s: %v", path, err)
		return p
	}
	p.exists, p.dicts = true, f.Dicts
	return p
}

// Off reports whether this dictionary is excluded from "All dictionaries".
// It matches on id first and path second, so a record written before a move
// still speaks for the dictionary it was written about.
func (p *Prefs) Off(id, path string) bool {
	p.mu.RLock()
	defer p.mu.RUnlock()
	for _, d := range p.dicts {
		if d.ID == id || (d.Path != "" && samePath(d.Path, path)) {
			return d.Off
		}
	}
	return false
}

// Snapshot returns the records and whether a state file existed at startup.
func (p *Prefs) Snapshot() (dicts []DictPref, exists bool) {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return append([]DictPref(nil), p.dicts...), p.exists
}

// Replace stores a new list and writes it. The write is temp-file + rename, so
// a crash mid-save leaves the previous state intact rather than a truncated
// file that reads as "nothing was ever configured".
func (p *Prefs) Replace(dicts []DictPref) error {
	p.mu.Lock()
	p.dicts, p.exists = append([]DictPref(nil), dicts...), true
	path := p.path
	data, err := json.MarshalIndent(prefsFile{Version: prefsVersion, Dicts: p.dicts}, "", "  ")
	p.mu.Unlock()
	if err != nil || path == "" {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".state-*.json")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(append(data, '\n')); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}

// samePath compares two dictionary paths the way the filesystem would be asked
// to: absolute and cleaned. Case is kept significant — macOS and Windows would
// disagree, and a false match is worse than a missed one here.
func samePath(a, b string) bool {
	if a == "" || b == "" {
		return false
	}
	if aa, err := filepath.Abs(a); err == nil {
		a = aa
	}
	if bb, err := filepath.Abs(b); err == nil {
		b = bb
	}
	return filepath.Clean(a) == filepath.Clean(b)
}

// heal re-attaches stored records to the dictionaries as they exist NOW and
// persists the repair when anything moved.
//
// The identity ladder is id → path → unique file name, each rung weaker than
// the last and each guarded against collisions: a record may claim an entry
// only if no stronger record claimed it first, and the file-name rung is used
// only when that name is unique on BOTH sides. That last guard is what keeps
// library entries (every one of them a "text.db") from swapping identities
// with each other.
//
// Records that match nothing are kept, not dropped. An unplugged drive is the
// common case, and forgetting a user's curation because a disk was asleep
// would be exactly the failure this whole file exists to prevent.
func (p *Prefs) heal(r *Registry) []DictPref {
	stored, _ := p.Snapshot()
	entries := r.all()

	byID := make(map[string]*entry, len(entries))
	byPath := make(map[string]*entry, len(entries))
	base := map[string][]*entry{}
	for _, e := range entries {
		byID[e.ID] = e
		if abs, err := filepath.Abs(e.Path); err == nil {
			byPath[filepath.Clean(abs)] = e
		} else {
			byPath[filepath.Clean(e.Path)] = e
		}
		b := strings.ToLower(filepath.Base(e.Path))
		base[b] = append(base[b], e)
	}

	out := append([]DictPref(nil), stored...)
	claimed := map[string]bool{}
	// strongest rung first, so a weaker match can never steal a live id
	for i, d := range out {
		if e, ok := byID[d.ID]; ok {
			claimed[e.ID] = true
			out[i].Path = e.Path
		}
	}
	changed := false
	for i, d := range out {
		if claimed[d.ID] {
			continue
		}
		var e *entry
		if abs, err := filepath.Abs(d.Path); err == nil && d.Path != "" {
			e = byPath[filepath.Clean(abs)]
		}
		if e == nil && d.Path != "" {
			if m := base[strings.ToLower(filepath.Base(d.Path))]; len(m) == 1 {
				e = m[0]
			}
		}
		if e == nil || claimed[e.ID] {
			continue
		}
		claimed[e.ID] = true
		out[i].ID, out[i].Path = e.ID, e.Path
		changed = true
	}
	if changed {
		if err := p.Replace(out); err != nil {
			logx.Warn("could not save %s: %v", p.path, err)
		}
	}
	return out
}

// merge folds the client's ordered list into the stored one. The client can
// only speak for the dictionaries it can see, so records it did not mention
// are RETAINED at the end rather than deleted: an unmounted drive must not
// cost the user the settings for everything on it.
func (p *Prefs) merge(r *Registry, want []DictPref) []DictPref {
	stored, _ := p.Snapshot()
	out := make([]DictPref, 0, len(want)+len(stored))
	seenID := map[string]bool{}
	seenPath := map[string]bool{}
	for _, d := range want {
		if d.ID == "" || seenID[d.ID] {
			continue
		}
		// the registry, not the client, is authoritative about where a
		// dictionary lives; the name is the client echoing our own label back
		if e, err := r.get(d.ID); err == nil {
			d.Path = e.Path
		}
		if d.Name == "" && d.Path != "" {
			d.Name = filepath.Base(d.Path)
		}
		seenID[d.ID] = true
		if d.Path != "" {
			seenPath[cleanAbs(d.Path)] = true
		}
		out = append(out, d)
	}
	for _, d := range stored {
		if seenID[d.ID] || (d.Path != "" && seenPath[cleanAbs(d.Path)]) {
			continue
		}
		out = append(out, d)
	}
	return out
}

func cleanAbs(p string) string {
	if abs, err := filepath.Abs(p); err == nil {
		return filepath.Clean(abs)
	}
	return filepath.Clean(p)
}

// GET /api/prefs — the enabled set and the order, healed against the
// dictionaries that exist right now. "exists" is false on a first run, which
// is the client's cue to adopt whatever an older build left in localStorage
// (once) instead of starting the user over.
func (s *Server) handlePrefs(w http.ResponseWriter, r *http.Request) {
	dicts := s.reg.prefs.heal(s.reg)
	_, exists := s.reg.prefs.Snapshot()
	writeJSON(w, map[string]any{"exists": exists, "dicts": dicts})
}

// PUT /api/prefs — replace the order and enabled set.
func (s *Server) handleSavePrefs(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Dicts []DictPref `json:"dicts"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&req); err != nil {
		http.Error(w, "bad request: "+err.Error(), http.StatusBadRequest)
		return
	}
	merged := s.reg.prefs.merge(s.reg, req.Dicts)
	if err := s.reg.prefs.Replace(merged); err != nil {
		http.Error(w, "could not save: "+err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, map[string]any{"exists": true, "dicts": merged})
}
