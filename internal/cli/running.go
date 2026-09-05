// Copyright (C) 2026 glowinthedark
//
// SPDX-License-Identifier: GPL-3.0-or-later

package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/wuweidict/wudict/internal/dict"
	"github.com/wuweidict/wudict/internal/logx"
	"github.com/wuweidict/wudict/internal/server"
)

// runningInstance describes a wudict already serving on the address this
// launch wanted. Only what the banner needs.
type runningInstance struct {
	Version string
	Roots   []struct {
		Path   string `json:"path"`
		Count  int    `json:"count"`
		Total  int    `json:"total"`
		Exists bool   `json:"exists"`
	} `json:"roots"`
	LibDir     string `json:"libDir"`
	Prepared   int    `json:"prepared"`
	UseCached  bool   `json:"useCached"`
	Total      int    `json:"total"`
	ConfigPath string `json:"configPath"`

	// Restricted means the instance answered, and answered that we may not
	// ask further: it requires an access key this launch did not present.
	// Identity is still established - the Server header is not behind the
	// key - but nothing below it is, so every field above is zero and must
	// not be read as fact.
	Restricted bool `json:"-"`
}

// probeRunning asks whatever holds addr whether it is a wudict, and if so
// what it is serving.
//
// The identity check is the whole point: a port can be held by anything, and
// pointing the user's browser at an unknown local service on the strength of a
// port number would be worse than the plain error. Only a response carrying
// our Server header counts; anything else - a different app, no answer, a
// timeout - leaves the caller to report the port as simply occupied.
func probeRunning(addr string) (*runningInstance, bool) {
	client := &http.Client{Timeout: 700 * time.Millisecond}
	host := addr
	// A wildcard bind answers on loopback too, and loopback is the one
	// interface guaranteed to be up - so ask there. Both spellings of
	// "wildcard" have to be recognised: the string test this replaces knew
	// "0.0.0.0" and missed "::" and "[::]", and on those a second launch got
	// a bind error it could not explain instead of the running instance. A
	// CONCRETE non-loopback bind is left alone on purpose: nothing is
	// listening on loopback then.
	if h, port, err := net.SplitHostPort(addr); err == nil {
		if ip := net.ParseIP(h); ip != nil && ip.IsUnspecified() {
			host = net.JoinHostPort("127.0.0.1", port)
		}
	}
	resp, err := client.Get("http://" + host + "/api/config")
	if err != nil {
		return nil, false
	}
	defer resp.Body.Close()
	id := resp.Header.Get("Server")
	if !strings.HasPrefix(id, server.ServerHeader) {
		return nil, false
	}
	inst := &runningInstance{Version: strings.TrimPrefix(strings.TrimPrefix(id, server.ServerHeader), "/")}
	if resp.StatusCode == http.StatusUnauthorized {
		inst.Restricted = true
		return inst, true
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err == nil {
		_ = json.Unmarshal(body, inst) // details are a bonus; identity already established
	}
	return inst, true
}

// sameFolders reports whether the running instance is serving exactly the
// folders this launch asked for. When it is not, the flags just typed are not
// merely ignored - they name a different library, which is what the user
// actually needs to be told.
func sameFolders(want []string, inst *runningInstance) bool {
	// Nothing was disclosed, so nothing is known to differ. Claiming a
	// mismatch here would print a loud warning about folders on the strength
	// of an empty struct.
	if inst.Restricted {
		return true
	}
	if len(want) != len(inst.Roots) {
		return false
	}
	have := make(map[string]bool, len(inst.Roots))
	for _, r := range inst.Roots {
		have[dict.CanonPath(r.Path)] = true
	}
	for _, w := range want {
		if !have[dict.CanonPath(w)] {
			return false
		}
	}
	return true
}

// announceRunning prints the "this launch did nothing" banner. Loud on
// purpose: the user asked for a program to start, and instead an older one -
// with older settings - is about to appear in their browser. Anything quieter
// would leave them debugging why their new --dict-dir had no effect.
func announceRunning(inst *runningInstance, url string, wantDirs []string, willOpen bool) {
	// logx.Output(), not os.Stderr: a GUI launch has already given its console
	// back, and this banner is the only record of why that launch did nothing.
	// It matches printStartup, which formats its own block the same way.
	out := logx.Output()
	const bar = "========================================================================"
	fmt.Fprintf(out, "\n%s\n", bar)
	fmt.Fprintf(out, "  wudict IS ALREADY RUNNING  --  this launch is doing nothing\n")
	fmt.Fprintf(out, "%s\n\n", bar)

	if inst.Version != "" && inst.Version != Version {
		fmt.Fprintf(out, "  running version   %s   (you launched %s)\n", inst.Version, Version)
	}
	fmt.Fprintf(out, "  already serving   %s\n", url)
	if inst.Restricted {
		// Everything below this line is unknown, not absent: the running
		// instance is asking for its access key, and this launch did not
		// present one. Saying so is the difference between "it is running"
		// and "it is running and something is wrong with your key".
		fmt.Fprintf(out, "                    (it asks for an access key this launch did not have)\n")
	}
	if inst.Total > 0 {
		fmt.Fprintf(out, "  dictionaries      %s\n", plural(inst.Total, "dictionary", "dictionaries"))
	}
	for i, r := range inst.Roots {
		label := "  from              "
		if i > 0 {
			label = "                    "
		}
		fmt.Fprintf(out, "%s%s\n", label, r.Path)
	}
	if inst.LibDir != "" {
		state := "not in use"
		if inst.UseCached {
			state = "in use"
		}
		fmt.Fprintf(out, "  library           %s  (%d prepared, %s)\n", inst.LibDir, inst.Prepared, state)
	}

	fmt.Fprintf(out, "\n  !! The options you passed just now were NOT applied. The running\n")
	fmt.Fprintf(out, "     instance keeps the settings it started with.\n")
	if len(wantDirs) > 0 && !sameFolders(wantDirs, inst) {
		fmt.Fprintf(out, "\n  !! You asked for different folders:\n")
		for _, d := range wantDirs {
			fmt.Fprintf(out, "       %s\n", d)
		}
		fmt.Fprintf(out, "     Those are NOT what you are about to see.\n")
	}
	fmt.Fprintf(out, "\n  To use the new settings, stop the running instance first (Ctrl-C in\n")
	fmt.Fprintf(out, "  its terminal), or start a second one on another port:  --port %s\n", nextPort(portOf(url)))
	if willOpen {
		fmt.Fprintf(out, "\n  Opening the running instance in your browser...\n")
	}
	fmt.Fprintf(out, "%s\n\n", bar)
}

// portOf pulls the port back out of the URL for the --port hint.
func portOf(url string) string {
	url = strings.TrimSuffix(strings.TrimPrefix(url, "http://"), "/")
	if i := strings.LastIndex(url, ":"); i >= 0 {
		return url[i+1:]
	}
	return url
}
