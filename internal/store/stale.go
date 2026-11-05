// Copyright (C) 2026 glowinthedark
//
// SPDX-License-Identifier: GPL-3.0-or-later

package store

import (
	"os"
	"strconv"
	"strings"

	"github.com/wuweidict/wudict/internal/dict"
	"github.com/wuweidict/wudict/internal/fsx"
)

// A prepared dictionary is frozen output of the code that built it. When that
// code changes, the data on disk does not follow, and nothing about it fails:
// it keeps answering queries with whatever the old code wrote. The versions
// below are how the running code says what it would write now, and Stale is
// the one place that compares the two.
//
// Each stamp covers one layer, so a bump invalidates exactly what it changed:
//
//	ingest_version   this file's IngestPlan: the schema's use, StripHTML, the
//	                 alias and redirect rules, body normalization
//	reader_version   one format's Reader (dict.RegisterReaderVersion)
//	markup_version   internal/artmark's role vocabulary (MarkupStale)
//	media_version    IngestMedia
//
// A missing stamp means the database predates the stamp, and is read as
// version 1 - the behaviour in force when the stamp was introduced - so the
// day this ships does not declare an entire library outdated.
//
// Stale reports; it never rebuilds. Rebuilding costs minutes to hours on a
// large library and is started only by the user (wudict reindex, or the
// panel's Rebuild), never on anyone's behalf (docs.local/PERF.md).

// IngestVersion identifies IngestPlan's behaviour. Bump it in the same commit
// as any change to what an ingest writes for the same Reader output; the
// golden tests in the format packages fail on such a change and say so.
const IngestVersion = 1

// MediaVersion identifies IngestMedia's behaviour, bumped the same way.
const MediaVersion = 1

// Reason is why a prepared dictionary no longer matches what this build would
// prepare from its source. The values are stable: the CLI prints them and the
// HTTP API reports them.
type Reason string

const (
	ReasonSchema Reason = "schema" // unreadable, or another schema: not usable at all
	ReasonSource Reason = "source" // the source file was edited or replaced
	ReasonAbbrev Reason = "abbrev" // the DSL abbreviation glossary changed
	ReasonIngest Reason = "ingest" // built by an older IngestPlan
	ReasonReader Reason = "reader" // built by an older Reader for its format
	ReasonMarkup Reason = "markup" // articles written with an older role markup
	ReasonCodec  Reason = "codec"  // articles stored with another codec than COMPRESSION (D191)
	ReasonMedia  Reason = "media"  // media.db unreadable, unpaired or outdated
)

// stampOf reads an integer version stamp, defaulting to def when the key is
// absent (a database written before the stamp existed) or not a number.
func stampOf(m map[string]string, key string, def int) int {
	if s := m[key]; s != "" {
		if n, err := strconv.Atoi(s); err == nil {
			return n
		}
	}
	return def
}

// Outdated is the code-version half of Stale, for a meta table already in
// hand: the reasons that depend only on which code built the database, not on
// the files beside it. Cheap - no I/O.
//
// Ingest and reader versions are compared as "older than": a library prepared
// by a NEWER build is not something this one can improve by rebuilding it.
// Markup keeps its established exact-match rule (MarkupStale), since either
// direction changes what a stylesheet meets.
func Outdated(m map[string]string) []Reason {
	var out []Reason
	if stampOf(m, "ingest_version", 1) < IngestVersion {
		out = append(out, ReasonIngest)
	}
	if rv := dict.ReaderVersion(m["format"]); rv > 0 && stampOf(m, "reader_version", 1) < rv {
		out = append(out, ReasonReader)
	}
	if MarkupStale(m) {
		out = append(out, ReasonMarkup)
	}
	if codecStale(m) {
		out = append(out, ReasonCodec)
	}
	return out
}

// codecStale reports that the articles are stored with another codec than
// new ingests write (COMPRESSION, D191): a DEFLATE database under zstd is
// offered a rebuild, which runs only when the user asks for one (D151). Only the codec counts, not its level: a level is how hard,
// not what (D182 Am. 2). A database that recorded no body_encoding is not
// judged.
func codecStale(m map[string]string) bool {
	enc := m["body_encoding"]
	if enc == "" {
		return false
	}
	codec, _, _ := strings.Cut(enc, "-")
	if codec == "plain" {
		codec = codecNone
	}
	return codec != codecSetting.name
}

// Prepared is one prepared database read once. Every question asked about a
// prepared dictionary - can this build use it, what was it built with, is it
// outdated, does its media pair with it - is answered from one of these, so a
// caller asking several of them pays for one read of the meta table, not one
// per question.
type Prepared struct {
	TextDB string
	Meta   map[string]string // nil: missing, or not a readable database
	Schema int               // PRAGMA user_version; meaningful only with Meta
	Plan   Plan              // what it was built with; zero when Meta is nil
	Media  bool              // a media.db lies beside it (paired or not)
}

// Inspect reads textDB's meta table and notes whether a media.db lies beside
// it. A missing or unreadable file is a Prepared with no Meta, never an error:
// to every caller that is simply "not prepared".
func Inspect(textDB string) Prepared {
	p := Prepared{TextDB: textDB}
	if m, schema, err := ReadMetaSchema(textDB); err == nil {
		p.Meta, p.Schema, p.Plan = m, schema, PlanFromMeta(m)
	}
	if sib := MediaSibling(textDB); sib != "" && fsx.FileExists(sib) {
		p.Media = true
	}
	return p
}

// Usable reports that this build can open it: readable, and this schema.
func (p Prepared) Usable() bool { return p.Meta != nil && textSchemaOK(p.Schema) }

// Current reports that it is usable and was built from srcPath as the file is
// now. A missing source is not a change: the prepared data stands on its own.
func (p Prepared) Current(srcPath string) bool {
	return p.Usable() && !sourceChangedMeta(p.Meta, srcPath)
}

// MediaPaired reports that the media.db beside it serves it: readable, packed
// by the current IngestMedia, and stamped with this text.db's dict_uuid. An
// unpaired media.db is invisible to Store.mediaDB, so for every purpose that
// matters it is not packed. Reads the media.db's meta; ask once.
func (p Prepared) MediaPaired() bool {
	return p.Media && p.Meta != nil && !mediaStale(MediaSibling(p.TextDB), p.Meta["dict_uuid"])
}

// Stale reports every reason it no longer matches what this build would
// prepare from srcPath now; empty means current. srcPath may be "" or gone:
// the reasons that need the source are then skipped and the rest reported.
func (p Prepared) Stale(srcPath string) []Reason {
	out := p.TextStale(srcPath)
	if p.Usable() && p.Media && !p.MediaPaired() {
		out = append(out, ReasonMedia)
	}
	return out
}

// TextStale is Stale for the text.db alone: the reasons a rebuild of the text
// answers. Media is judged after the text is current, because a text rebuild
// can be what unpairs it.
func (p Prepared) TextStale(srcPath string) []Reason {
	if !p.Usable() {
		return []Reason{ReasonSchema}
	}
	var out []Reason
	if srcPath != "" {
		if sourceChangedMeta(p.Meta, srcPath) {
			out = append(out, ReasonSource)
		}
		companion, _ := dict.AbbrevCompanion(srcPath)
		if abbrevChangedMeta(p.Meta, companion) {
			out = append(out, ReasonAbbrev)
		}
	}
	return append(out, Outdated(p.Meta)...)
}

// Stale is Inspect(textDB).Stale(srcPath).
func Stale(textDB, srcPath string) []Reason { return Inspect(textDB).Stale(srcPath) }

// KeptPlan is the Plan to REBUILD an existing text.db with: whatever it was
// built with, so a rebuild the user did not ask to change never changes it.
// An unreadable or missing database gets the default (headwords only, D24),
// which is also what a dictionary never prepared gets.
func KeptPlan(textDB string) Plan { return Inspect(textDB).Plan }

// MediaPaired is Inspect(textDB).MediaPaired().
func MediaPaired(textDB string) bool { return Inspect(textDB).MediaPaired() }

// ReadMetaSchema reads a wudict database's meta table together with its schema
// version (PRAGMA user_version). An error means the file cannot be read as a
// SQLite database with a meta table at all.
func ReadMetaSchema(dbPath string) (map[string]string, int, error) {
	if _, err := os.Stat(dbPath); err != nil {
		return nil, 0, err // never let the driver create an empty file
	}
	db, err := openRO(dbPath)
	if err != nil {
		return nil, 0, err
	}
	defer db.Close()
	var ver int
	if err := db.QueryRow("PRAGMA user_version").Scan(&ver); err != nil {
		return nil, 0, err
	}
	m, err := readMeta(db)
	if err != nil {
		return nil, ver, err
	}
	return m, ver, nil
}

// PlanFromMeta is the Plan a prepared database was built with, read back from
// its meta. The one reader of `ingest_level`.
func PlanFromMeta(m map[string]string) Plan {
	return Plan{FullText: m["ingest_level"] != levelHeadwords}
}

// ingestLevel is PlanFromMeta's inverse for `ingest_level`.
func ingestLevel(p Plan) string {
	if p.FullText {
		return levelText
	}
	return levelHeadwords
}

// mediaStale reports that a media.db cannot serve the text.db stamped with
// uuid: unreadable, another schema, another dictionary's, or packed by an
// older IngestMedia.
func mediaStale(mediaDB, uuid string) bool {
	m, schema, err := ReadMetaSchema(mediaDB)
	if err != nil || schema != sidecarSchema {
		return true
	}
	return uuid == "" || m["dict_uuid"] != uuid || stampOf(m, "media_version", 1) < MediaVersion
}

// keptUUID is the dict_uuid an ingest into dbPath should reuse, or "" for a
// fresh one. Reused only while the database there was built from the same,
// unchanged source: then the media.db beside it was packed from the same
// resources and stays paired across the rebuild, instead of being orphaned by
// a rebuild that did not touch a single resource. A changed source gets a new
// identity, and its media is repacked by whoever rebuilt it.
func keptUUID(dbPath, srcPath string) string {
	m, schema, err := ReadMetaSchema(dbPath)
	if err != nil || !textSchemaOK(schema) || srcPath == "" {
		return ""
	}
	if !fsx.SamePath(m["source_path"], srcPath) || sourceChangedMeta(m, srcPath) {
		return ""
	}
	return m["dict_uuid"]
}
