package server

import (
	"encoding/json"
	"net/http"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/wuweidict/wudict/internal/facet"
	"github.com/wuweidict/wudict/internal/fsx"
	"github.com/wuweidict/wudict/internal/logx"
)

// StateFile holds the part of the UI that describes the COLLECTION rather than
// the browser looking at it: which dictionaries are searched, in what order,
// and how big their text is set (see UIPrefs). Everything that describes the
// browser instead - theme, wide mode, the last dictionary picked in the
// dropdown - stays in localStorage, where it belongs.
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

// prefsVersion is written to the file so a format change can be recognised
// rather than guessed at. Version 2 is the pinned list (D166): DictPref.Pin
// marks the dictionaries the user placed, and every other record's position
// is no longer an order. A version-1 list is ordered whole, and only the page
// converts it - it is the page that compares names (dictionary titles, in its
// own collation), so the server keeps the version it read until the page has
// sent a list of its own.
const prefsVersion = 2

// DictPref is one dictionary's remembered state. The path is what makes this
// survivable: ids are sha256(path)[:12] (see pathID), so moving a dictionary
// folder changes every id at once. Recording the path - and, through it, the
// file name - lets the state be re-attached instead of silently reset.
type DictPref struct {
	ID   string `json:"id"`
	Path string `json:"path"`
	Name string `json:"name,omitempty"` // display name, for a readable file
	Off  bool   `json:"off,omitempty"`  // excluded from "All dictionaries"
	// Pin places the dictionary at the head of the list, in the order of
	// the pinned records; the others are listed A-Z (D166).
	Pin bool `json:"pin,omitempty"`
}

// UIPrefs is the part of the reading experience that belongs to the PERSON
// rather than to the browser in front of them. The split at the top of this
// file sends theme and wide mode to localStorage because they are per-device
// view choices; text size is not one. A reader who needs 24px needs it on the
// phone that reaches this same server too, and localStorage — keyed by origin
// — would hand them 15px there, and again the first time SERVER_PORT changes.
type UIPrefs struct {
	FontSize int `json:"fontSize,omitempty"` // article text, px; 0 = the default

	// HLOff turns OFF marking of full-text matches inside articles. It is
	// negating for the same reason DictPref.Off is: the default is on, and a
	// default that is the zero value is a default a hand-written or
	// half-written state.json cannot get wrong. An absent key is the feature
	// working, never the feature silently missing.
	//
	// It is remembered state for the UI, not an instruction to the server: the
	// search path never reads it. The SPA turns it into ?hl= on the request,
	// which is what keeps an API client that never saw this file from
	// inheriting a preference set in somebody's browser.
	HLOff bool `json:"hlOff,omitempty"`

	// FastFirst expands whichever dictionary answers first, instead of the
	// first dictionary in the user's own order that has a result (D73, D142;
	// default flipped in D144).
	//
	// Spelled as the opt-OUT, the way HLOff is, because the default is now the
	// reader's own order: the list they arranged is the answer to "which of
	// these do I want", and a race winner is not that answer. So an absent key
	// is the default strategy, which is the rule both flags obey - a
	// half-written state.json must not be able to invent a setting the user
	// never made.
	//
	// Like HLOff it is remembered state for the UI and not an instruction to
	// the server: /api/search knows nothing about it, because which section a
	// reader unfolds is a property of the reader, not of the answer.
	FastFirst bool `json:"fastFirst,omitempty"`

	// OpenN is how many sections open by themselves under the reader's own
	// order: the first OpenN dictionaries in that order that have a result.
	// 0 (absent) and 1 are both the default, one; there is no upper limit,
	// because the reader may trade speed for it. OpenAll opens every one that
	// has a result. The page's menu sets these and FastFirst together, and
	// FastFirst, which opens a single section, wins if a hand edit sets both.
	// Remembered state for the UI, like FastFirst: the server never reads it.
	OpenN   int  `json:"openN,omitempty"`
	OpenAll bool `json:"openAll,omitempty"`

	// SpeakOff turns OFF the read-aloud button that appears over a text
	// selection in an article. Negated like HLOff: the feature is on unless
	// the person said otherwise. Which VOICE reads is not here - the voices
	// are whatever the device in front of them has installed, so that choice
	// stays in that browser's localStorage.
	SpeakOff bool `json:"speakOff,omitempty"`

	// GroupsOff lists the picker sections the person has hidden, by facet id:
	// "lang", "pair", or a groups.ini section lower-cased (internal/facet).
	// Negated for the same reason as the flags above: every section the
	// library supports is shown unless the person said otherwise, and a facet
	// added tomorrow appears without a migration. Ids are kept opaque here - the server never groups anything,
	// and an id no facet uses any more simply hides nothing.
	GroupsOff []string `json:"groupsOff,omitempty"`

	// Full opens an article's secondary zone (DSL [*], wu-sec: examples and
	// links to sub-entries) shown instead of hidden. Spelled positively: the
	// brief view is Lingvo's own default, so the zero value keeps it - the
	// rule every flag here obeys is that an absent key is the standing
	// default, not that the word is always a negation. It is the reader's
	// last choice on a section's switch, and sets how the next section
	// opens; the server never reads it.
	Full bool `json:"full,omitempty"`

	// Mode is the search mode the reader last picked: "exact", "contains" or
	// "fts". Empty is prefix, the default, so an absent key keeps it. It is the
	// reader's habit, and which modes answer at all is a fact about this
	// server's library, so it lives here and not in the browser. The page
	// writes it only when the reader picks a mode, never when a link or the
	// history sets one; the server never reads it.
	Mode string `json:"mode,omitempty"`
}

// Article text-size bounds. The ceiling is deliberately past what the layout
// was drawn for: a reader who needs 32px needs it more than the design needs
// to stay pretty, and nothing in the article surface loses text when it grows
// — it only gets taller.
const (
	FontSizeMin = 11
	FontSizeMax = 32
)

// normalize repairs what a hand-edited file may carry. Clamping rather than
// zeroing, because "40" is a legible statement of intent that deserves the
// nearest size we offer, not a silent snap back to the default.
func (u *UIPrefs) normalize() {
	if u == nil {
		return
	}
	if u.FontSize != 0 {
		u.FontSize = max(FontSizeMin, min(FontSizeMax, u.FontSize))
	}
	u.GroupsOff = facetIDs(u.GroupsOff)
	if u.OpenN <= 1 {
		u.OpenN = 0 // one, the default, is stored as absent
	}
	switch u.Mode {
	case "exact", "contains", "fts":
	default: // prefix, and what a hand edit may carry (the retired "fuzzy", D16)
		u.Mode = ""
	}
}

// facetIDs bounds a hand-edited or hostile groupsOff to what a facet id can
// be: short, non-blank, unique, sorted so an unchanged set writes an unchanged
// file. Always a fresh slice, so a stored record never shares its backing
// array with the request it came from.
func facetIDs(in []string) []string {
	const maxIDs, maxLen = 32, 64 // ids are section names the user writes (D162)
	var out []string
	for _, id := range in {
		id = strings.TrimSpace(id)
		if id == "" || len(id) > maxLen || slices.Contains(out, id) {
			continue
		}
		if out = append(out, id); len(out) == maxIDs {
			break
		}
	}
	slices.Sort(out)
	return out
}

type prefsFile struct {
	Version int        `json:"version"`
	UI      *UIPrefs   `json:"ui,omitempty"`
	Dicts   []DictPref `json:"dicts"` // array ORDER is the user's order
}

// Prefs is the state file (state.json), an ownedFile (ownedfile.go): read
// bounded and re-read when it changes on disk, so a hand edit made while the
// app runs is adopted - including by the next save, which re-reads first and
// so builds on it instead of writing it away. A Prefs with an empty path is
// in-memory only: it answers questions and forgets on exit, which is what
// tests and a home-less environment need.
type Prefs struct {
	file  ownedFile[prefsFile]
	write sync.Mutex // a save's read-modify-write, whole
}

// maxPrefsBytes bounds state.json: thousands of dictionaries' worth, and a
// pipe or a device in its place is refused (fsx).
const maxPrefsBytes = 4 << 20

// LoadPrefs reads the state file. It never fails: a missing file is the normal
// first run, and an unreadable or corrupt one must not stop the app from
// serving dictionaries - the worst case is that the user re-curates a list.
func LoadPrefs(path string) *Prefs {
	p := &Prefs{}
	p.file = ownedFile[prefsFile]{
		name: StateFile, path: func() string { return path }, max: maxPrefsBytes,
		recheck: time.Second, perm: 0o600, parse: parsePrefs,
		absent: func() prefsFile { return prefsFile{} },
	}
	if st := p.file.now(); st.unusable() {
		logx.Warn("ignoring %s: %s", path, st.why)
	}
	return p
}

// parsePrefs reads a state file. A malformed one is announced (once per
// content: it is parsed only when it changed) and treated as no state - the
// next save writes over it, so this warning is the user's chance to notice.
func parsePrefs(text string) (prefsFile, []facet.Problem) {
	var f prefsFile
	if err := json.Unmarshal([]byte(text), &f); err != nil {
		logx.Warn("ignoring malformed %s: %v", StateFile, err)
		return prefsFile{}, []facet.Problem{{Msg: "malformed: " + err.Error()}}
	}
	f.UI.normalize()
	return f, nil
}

// data is the record in effect and whether it is a real one: a usable,
// well-formed file, or one this process wrote.
func (p *Prefs) data() (prefsFile, bool) {
	st := p.file.now()
	return st.val, st.exists && !st.unusable() && len(st.problems) == 0
}

// UI returns a copy of the UI record, or nil when nothing has been set. A copy
// because the caller marshals it while other requests may be writing.
func (p *Prefs) UI() *UIPrefs {
	f, _ := p.data()
	if f.UI == nil {
		return nil
	}
	u := *f.UI
	return &u
}

// Off reports whether this dictionary is excluded from "All dictionaries".
// It matches on id first and path second, so a record written before a move
// still speaks for the dictionary it was written about.
func (p *Prefs) Off(id, path string) bool {
	f, _ := p.data()
	for _, d := range f.Dicts {
		if d.ID == id || (d.Path != "" && fsx.SamePath(d.Path, path)) {
			return d.Off
		}
	}
	return false
}

// Snapshot returns the records and whether a state file exists.
func (p *Prefs) Snapshot() (dicts []DictPref, exists bool) {
	f, ok := p.data()
	return append([]DictPref(nil), f.Dicts...), ok
}

// Replace stores a new list and writes it, atomically (fsx.WriteAtomic): a
// crash mid-save leaves the previous state intact rather than a truncated
// file that reads as "nothing was ever configured".
func (p *Prefs) Replace(dicts []DictPref) error { return p.update(dicts, nil, false) }

// update is Replace plus an optional UI patch: the JSON object of the UI
// fields to change. A nil dicts keeps the stored list, decided here under the
// write lock rather than by the caller re-sending a snapshot it read before,
// which a save from another page could land between. Absent or null means "not mentioned" and leaves the stored
// record untouched, and inside the object the rule is the same per field - a
// key that is not sent keeps its stored value, and only a key sent as false,
// 0 or [] clears one. Two pages on one server (two tabs, or the Android app and
// its lookup popup, each a WebView of its own) each hold a copy of these
// settings read when they loaded; a page that sends only what it changed
// cannot carry its stale copy of the others back over a newer choice.
//
// One write, not two, so the two settings can never disagree on disk. It
// starts from the file as it is NOW (fresh): a hand edit made a moment ago is
// kept wherever this update does not speak.
//
// The version moves to prefsVersion only with a list the PAGE sent (page),
// or when there is no list to be in an older format. A list the server
// repaired itself (heal) keeps the version it was read with: that list is
// still in the old format, and stamping it would tell the page it has
// nothing to convert.
func (p *Prefs) update(dicts []DictPref, ui json.RawMessage, page bool) error {
	p.write.Lock()
	defer p.write.Unlock()
	f := p.file.fresh().val
	if dicts != nil {
		f.Dicts = append([]DictPref(nil), dicts...)
	}
	if (page && dicts != nil) || len(f.Dicts) == 0 {
		f.Version = prefsVersion
	}
	if mentioned(ui) {
		var u UIPrefs
		if f.UI != nil {
			u = *f.UI
			// json decodes an array into the slice it finds, in place: without
			// the clone the patch would write into the record the file cache
			// still holds
			u.GroupsOff = slices.Clone(u.GroupsOff)
		}
		if err := json.Unmarshal(ui, &u); err != nil {
			return err
		}
		u.normalize()
		f.UI = &u
	}
	data, err := json.MarshalIndent(f, "", "  ")
	if err != nil {
		return err
	}
	// replace: the app owns this file, and a corrupt one was announced
	_, err = p.file.save(string(data)+"\n", true)
	return err
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
	// The other half of the file-name rung's guard: the name has to be unique
	// among the STORED records too. One
	// prepared dictionary and a handful of dead "…/text.db" records - the
	// residue of library folders the user has since removed - is the shape
	// where counting only the registry side lets a dead record adopt the live
	// entry, inheriting its off switch and its place in the order. Records a
	// stronger rung already claimed are counted as well: a name two records
	// share is ambiguous evidence whichever of them answered to it first.
	storedBase := map[string]int{}
	for _, d := range stored {
		if d.Path != "" {
			storedBase[strings.ToLower(filepath.Base(d.Path))]++
		}
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
			b := strings.ToLower(filepath.Base(d.Path))
			if m := base[b]; len(m) == 1 && storedBase[b] == 1 {
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
			logx.Warn("could not save %s: %v", p.file.path(), err)
		}
	}
	return out
}

// merge folds the client's ordered list into the stored one. The client can
// only speak for the dictionaries it can see, so records it did not mention
// are RETAINED rather than deleted: an unmounted drive must not cost the user
// the settings for everything on it. A retained pinned record keeps its rank
// among the pinned: it follows the record that was pinned before it and still
// is, or leads when none was - so a pinned dictionary on a drive that is away
// is back at its rank when the drive is (D166). A retained unpinned record
// has no rank, and goes last.
func (p *Prefs) merge(r *Registry, want []DictPref) []DictPref {
	stored, _ := p.Snapshot()
	storedPath := make(map[string]string, len(stored))
	for _, d := range stored {
		if d.Path != "" {
			storedPath[d.ID] = d.Path
		}
	}
	sent := make([]DictPref, 0, len(want))
	seenID := map[string]bool{}
	seenPath := map[string]string{} // cleaned path -> the id it was sent under
	for _, d := range want {
		if d.ID == "" || seenID[d.ID] {
			continue
		}
		// the registry, not the client, is authoritative about where a
		// dictionary lives; the name is the client echoing our own label back.
		// For one the registry does not list right now - a page working from
		// a list it cached, sent before the rescan that dropped it - the
		// stored record is the next best: a save never loses a known path.
		if e, err := r.get(d.ID); err == nil {
			d.Path = e.Path
		} else if d.Path == "" {
			d.Path = storedPath[d.ID]
		}
		if d.Name == "" && d.Path != "" {
			d.Name = filepath.Base(d.Path)
		}
		seenID[d.ID] = true
		if d.Path != "" {
			seenPath[cleanAbs(d.Path)] = d.ID
		}
		sent = append(sent, d)
	}
	pinnedNow := map[string]bool{}
	for _, d := range sent {
		if d.Pin {
			pinnedNow[d.ID] = true
		}
	}
	// after[id] holds the retained pinned records that followed the sent
	// pinned record id in the stored order; lead holds those no sent pinned
	// record preceded.
	after := map[string][]DictPref{}
	var lead, tail []DictPref
	prev := ""
	for _, d := range stored {
		id, sentToo := d.ID, seenID[d.ID]
		if !sentToo && d.Path != "" {
			id, sentToo = seenPath[cleanAbs(d.Path)] // sent under its current id
		}
		switch {
		case sentToo:
			if pinnedNow[id] {
				prev = id
			}
		case !d.Pin:
			tail = append(tail, d)
		case prev == "":
			lead = append(lead, d)
		default:
			after[prev] = append(after[prev], d)
		}
	}
	out := make([]DictPref, 0, len(sent)+len(stored))
	out = append(out, lead...)
	for _, d := range sent {
		out = append(out, d)
		out = append(out, after[d.ID]...)
	}
	return append(out, tail...)
}

// mentioned reports whether a request carried a UI patch: an absent key and
// an explicit null both leave the stored record alone.
func mentioned(ui json.RawMessage) bool {
	return len(ui) > 0 && string(ui) != "null"
}

func cleanAbs(p string) string {
	if abs, err := filepath.Abs(p); err == nil {
		return filepath.Clean(abs)
	}
	return filepath.Clean(p)
}

// GET /api/prefs - the enabled set and the order, healed against the
// dictionaries that exist right now. "exists" is false on a first run, which
// is the client's cue to adopt whatever an older build left in localStorage
// (once) instead of starting the user over. "version" below prefsVersion is
// its cue to convert the list (D166).
func (s *Server) handlePrefs(w http.ResponseWriter, r *http.Request) {
	dicts := s.reg.prefs.heal(s.reg)
	f, exists := s.reg.prefs.data()
	writeJSON(w, map[string]any{"exists": exists, "version": f.Version, "dicts": dicts, "ui": s.reg.prefs.UI()})
}

// PUT /api/prefs - replace the order and enabled set when the request sends
// them, and change the UI fields it names (update). An absent or null dicts
// keeps the stored list: a page that changed only a UI setting must not carry
// its stale copy of the order back over another page's newer one.
func (s *Server) handleSavePrefs(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Dicts []DictPref      `json:"dicts"`
		UI    json.RawMessage `json:"ui"` // raw: which keys are present is the patch
	}
	if !decodeJSON(w, r, &req, 1<<20) {
		return
	}
	// A wrongly typed field is the caller's mistake (400), found before the
	// save rather than inside it, where it would read as the server's (500).
	if mentioned(req.UI) {
		if err := json.Unmarshal(req.UI, new(UIPrefs)); err != nil {
			httpErr(w, http.StatusBadRequest, "bad request: %v", err)
			return
		}
	}
	var merged []DictPref
	if req.Dicts != nil {
		merged = s.reg.prefs.merge(s.reg, req.Dicts)
	}
	if err := s.reg.prefs.update(merged, req.UI, true); err != nil {
		httpErr(w, http.StatusInternalServerError, "%s", "could not save: "+err.Error())
		return
	}
	dicts, _ := s.reg.prefs.Snapshot()
	if dicts == nil {
		dicts = []DictPref{} // [] on the wire, as before, never null
	}
	writeJSON(w, map[string]any{"exists": true, "dicts": dicts, "ui": s.reg.prefs.UI()})
}
