// Copyright (C) 2026 glowinthedark
//
// SPDX-License-Identifier: GPL-3.0-or-later

package intake

import (
	"errors"
	"net/url"
	"regexp"
	"strings"
)

// Google Drive file links.
//
// What people share is the file's PAGE, drive.google.com/file/d/<id>/view: a
// viewer drawn by script, which holds neither the file nor a link to it. The
// file itself is served, without signing in, from
//
//	https://drive.usercontent.google.com/download?id=<id>&export=download&confirm=t
//
// which answers HEAD with the size and Last-Modified, GET with the real file
// name in Content-Disposition, and Range with 206 - everything a download
// here needs. confirm=t answers the "too large to scan for viruses" page Drive
// puts in front of big files.
//
// So a Drive link is rewritten once, in Check, and is from then on an ordinary
// download. Shared FOLDERS are not handled: their listing exists only behind
// the Drive API (which needs an API key) or undocumented pages.
//
// What is lost is the file name in the URL: every Drive download is called
// "download". Two places depend on a URL's name, and both are told about it:
// a download's resume and reuse records, which use the file id instead
// (partBase), and a collection, which learns the name from the server's
// answer to its HEAD (describe).

// ErrDriveRefused is Drive sending a page where the file should be: not
// shared with "anyone with the link", deleted, or over Drive's download quota
// for a popular file.
var ErrDriveRefused = errors.New("Google Drive did not hand over the file: check that it is shared with " +
	"\"anyone with the link\", or try again later if many people downloaded it recently")

// driveDownload is the address Drive serves files from; a variable only so a
// test can stand a local server in its place.
var driveDownload = url.URL{Scheme: "https", Host: "drive.usercontent.google.com", Path: "/download"}

const driveDownloadHost = "drive.usercontent.google.com"

var (
	// /file/d/<id>/view, and /file/u/1/d/<id> from a second signed-in account
	driveFileRE = regexp.MustCompile(`^/file/(?:u/\d+/)?d/([A-Za-z0-9_-]{10,})(?:/|$)`)
	driveIDRE   = regexp.MustCompile(`^[A-Za-z0-9_-]{10,}$`)
)

// driveDirect returns the download address of a Google Drive file link, and
// false for anything else - a folder, a Google Doc, another site.
func driveDirect(u *url.URL) (*url.URL, bool) {
	host := strings.ToLower(u.Hostname())
	q := u.Query()
	id := ""
	switch host {
	case "drive.google.com":
		if m := driveFileRE.FindStringSubmatch(u.Path); m != nil {
			id = m[1]
		} else if u.Path == "/open" || u.Path == "/uc" {
			id = q.Get("id")
		}
	case "docs.google.com":
		if u.Path == "/uc" {
			id = q.Get("id")
		}
	case driveDownloadHost:
		if u.Path == "/download" || u.Path == "/uc" {
			id = q.Get("id")
		}
	}
	if id == "" && strings.EqualFold(u.Host, driveDownload.Host) && u.Path == driveDownload.Path {
		id = q.Get("id") // the address itself, or a test's stand-in for it
	}
	if !driveIDRE.MatchString(id) {
		return nil, false
	}
	v := url.Values{"id": {id}, "export": {"download"}, "confirm": {"t"}}
	// Files shared before Drive's 2021 security update need their resource
	// key to be reachable by link at all.
	if k := q.Get("resourcekey"); k != "" {
		v.Set("resourcekey", k)
	}
	d := driveDownload
	d.RawQuery = v.Encode()
	return &d, true
}

// driveID is the file id of a Drive download address, or "".
func driveID(u *url.URL) string {
	if u == nil || !strings.EqualFold(u.Host, driveDownload.Host) {
		return ""
	}
	if id := u.Query().Get("id"); driveIDRE.MatchString(id) {
		return id
	}
	return ""
}

// partBase names a download's .part and its reuse record. From the URL's own
// file name where it has one; a Drive download has none ("download" for every
// file), and two files of one collection would then resume into, and reuse,
// each other - so the id names those.
func partBase(u *url.URL) string {
	if id := driveID(u); id != "" {
		return "gdrive-" + id
	}
	if id, _, ok := megaLink(u); ok {
		return "mega-" + id
	}
	return safeDirName(nameFromURL(u))
}
