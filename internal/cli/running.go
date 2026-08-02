// Copyright (C) 2026 glowinthedark
//
// SPDX-License-Identifier: GPL-3.0-or-later

package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/legbehindneck/wudict/internal/dict"
	"github.com/legbehindneck/wudict/internal/server"
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
}

// probeRunning asks whatever holds addr whether it is a wudict, and if so
// what it is serving.
//
// The identity check is the whole point: a port can be held by anything, and
// pointing the user's browser at an unknown local service on the strength of a
// port number would be worse than the plain error. Only a response carrying
// our Server header counts; anything else — a different app, no answer, a
// timeout — leaves the caller to report the port as simply occupied.
func probeRunning(addr string) (*runningInstance, bool) {
	client := &http.Client{Timeout: 700 * time.Millisecond}
	host := addr
	if strings.HasPrefix(host, "0.0.0.0:") { // bound to every interface: ask loopback
		host = "127.0.0.1:" + strings.TrimPrefix(host, "0.0.0.0:")
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
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err == nil {
		_ = json.Unmarshal(body, inst) // details are a bonus; identity already established
	}
	return inst, true
}

// sameFolders reports whether the running instance is serving exactly the
// folders this launch asked for. When it is not, the flags just typed are not
// merely ignored — they name a different library, which is what the user
// actually needs to be told.
func sameFolders(want []string, inst *runningInstance) bool {
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
// purpose: the user asked for a program to start, and instead an older one —
// with older settings — is about to appear in their browser. Anything quieter
// would leave them debugging why their new --dict-dir had no effect.
func announceRunning(inst *runningInstance, url string, wantDirs []string, willOpen bool) {
	out := os.Stderr
	const bar = "========================================================================"
	fmt.Fprintf(out, "\n%s\n", bar)
	fmt.Fprintf(out, "  wudict IS ALREADY RUNNING  --  this launch is doing nothing\n")
	fmt.Fprintf(out, "%s\n\n", bar)

	if inst.Version != "" && inst.Version != Version {
		fmt.Fprintf(out, "  running version   %s   (you launched %s)\n", inst.Version, Version)
	}
	fmt.Fprintf(out, "  already serving   %s\n", url)
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
