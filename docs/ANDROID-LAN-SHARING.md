# Sharing the library over Wi-Fi — proposal, not a decision

**Status: WON'T DO (2026-09-05).** Not rejected on merit — rejected because the
button is the small end of a feature whose honest form is §4 option C, and that
is not wanted now. Kept in full so that if it is ever taken up, nothing here has
to be rediscovered.

**What was done instead:** the *Reachable from* row is now labelled *(testing
only)* and its hint states the observable limits — it lasts only while the app is
on screen, and the address changes when the phone rejoins the network — so a user
who tries it and finds it stopped has already been told why. Option D (showing the
address as text) was NOT taken: an address printed under a switch reads as an
invitation to use it for something, which is what the label now says it is not.

The trigger: the Advanced section of the Android settings screen (D101) has a
**Reachable from** switch that widens the server's bind address from
`127.0.0.1` to `0.0.0.0`. The convenience asked for is a **Share link** button
that appears when it is on, builds the LAN URL, and hands it to
`Intent.ACTION_SEND`.

## 1. The part that is trivial

Two things, both small and both already possible with what the app holds today.

**Finding the address.** Do *not* use `ConnectivityManager.getLinkProperties()`:
it needs `ACCESS_NETWORK_STATE`, which this app does not hold and which would
have to be declared and justified. `java.net.NetworkInterface` needs no
permission at all:

```java
// first site-local IPv4 on an up, non-loopback interface
for (NetworkInterface ni : Collections.list(NetworkInterface.getNetworkInterfaces())) {
    if (ni.isLoopback() || !ni.isUp()) continue;
    for (InetAddress a : Collections.list(ni.getInetAddresses())) {
        if (a instanceof Inet4Address && a.isSiteLocalAddress()) return a.getHostAddress();
    }
}
```

**Sharing it.** `ACTION_SEND`, `text/plain`, `"http://" + ip + ":" + port`,
wrapped in `createChooser`. ~15 lines including the "not on Wi-Fi" case.

If the proposal were only these two things it would be an afternoon. It is not.

## 2. What the button actually promises

A link is a promise that it will work when the recipient opens it. Every
mechanism below exists to make that promise true, and each is a real cost.

### 2.1 The server dies when the app closes

`ServerProcess.release(mayStop)` — `MainActivity` passes `isFinishing()`. Close
the app and the child is killed. So the shared link works until the sender
swipes the app away, which is the single most likely thing they do after
sending it. A link that dies on the sender's next gesture is worse than no
link: the recipient sees a connection refused and blames the software.

Making the promise true means the server must **outlive the UI on purpose**,
which is a foreground service with an ongoing notification — the platform's only
sanctioned way to say "this process is doing something for the user while
nothing is on screen".

### 2.2 The screen goes off and the CPU stops

Two distinct mechanisms, both real, often confused:

- **SoC suspend.** With the screen off and no wake lock, the device suspends.
  An incoming TCP SYN on an associated Wi-Fi interface *may* wake it, and on
  many devices, in doze, it will not. Requires `PARTIAL_WAKE_LOCK` to be
  reliable → `android.permission.WAKE_LOCK`.
- **Wi-Fi power save.** The radio drops to a low duty cycle and multicast is
  filtered. Unicast to an established association mostly survives; discovery
  (mDNS/NSD, §6) does not. `WifiManager.WifiLock` (`WIFI_MODE_FULL_LOW_LATENCY`
  or `WIFI_MODE_FULL_HIGH_PERF`) is the counter.
- **The app-standby / cached-app freezer** (API 31+) freezes the app's cgroup.
  The exec'd child inherits that cgroup at spawn. A foreground service in the
  same process keeps the whole group out of it.

None of these is optional if the link must work while the phone is in a pocket.

### 2.3 The power protocol works against us

`PowerSignal` tells the server `background` when no window is visible, and the
server obeys by dropping every reopenable handle (`internal/server/power.go`);
`restricted` closes prepared databases too. That is correct today — nobody is
looking. With a remote reader it is exactly wrong: the visible-window heuristic
no longer means "nobody is using this".

So LAN sharing needs a fourth caller of the power protocol, or a rule that
sharing pins `PowerActive`. Pinning it means the memory caps in D101 stay
allocated with the screen off, which is the battery and RSS bill the whole
`PowerBackground` design exists to avoid.

### 2.4 Foreground service type, and the Play declaration

`IndexService` is `dataSync`, which on API 34+ is subject to a **6 h / 24 h
cumulative timeout** — a sharing session is not a sync and would be stopped
mid-use. The defensible type is `connectedDevice`
(`FOREGROUND_SERVICE_CONNECTED_DEVICE`); `specialUse` is the fallback and
requires a free-text justification reviewed by Play. New permissions in the
manifest: `WAKE_LOCK`, `FOREGROUND_SERVICE_CONNECTED_DEVICE`, and
`CHANGE_WIFI_MULTICAST_STATE` only if §6 is ever wanted. Every one of them is a
line on the Play listing and a question at review.

### 2.5 The link is not stable

DHCP renews, the phone roams to another AP, Wi-Fi drops to mobile data. The IP
in the message the recipient still has in their chat app is then wrong, with no
way to tell them. Mitigations, in increasing cost: state the expiry in the share
text (honest, useless); re-share; mDNS (§6).

Also common and invisible from the app: **AP client isolation**, on by default on
most guest networks and many ISP routers. The link is correct, both devices are
on the same Wi-Fi, and it cannot work. Nothing in the app can detect this before
the recipient fails.

## 3. The security ramification, which is the real one

There is **no authentication anywhere in the HTTP surface**. The only access
control is `isLoopback(r)` on two things — reveal-in-file-manager
(`folders.go:216`) and delete unless `ALLOW_REMOTE_DELETE`
(`remove.go:237`). Everything else is open to whoever can reach the port:

| Reachable by a LAN peer | Effect |
|---|---|
| `GET /api/search`, `/api/dicts`, media | reads the whole library |
| `PUT /api/prefs` | rewrites the collection: order, enabled set |
| `POST /api/demand`, `GET /api/ingest`, `/api/rescan` | starts long CPU-heavy work on the phone |
| `GET /api/setup`, `/api/library`, `/api/config` | reads folder paths, i.e. parts of the filesystem layout |
| `DELETE /api/library` | refused unless `ALLOW_REMOTE_DELETE` — the one thing that is gated |

Today this is defensible because widening the bind address is a deliberate act
behind an Advanced section, under a warning, described as *"lets other machines
read your library, with no password"*. **A Share button changes its moral
status**: the app would be offering the act, one tap, in the same visual weight
as the rest of the screen. Under D102 (need-to-know) a control is an
endorsement; this one endorses something the app cannot make safe.

Making it safe is not small: a bearer token in the URL path or query, checked by
middleware, excluded from logs, rotated per session, and — because the page is
served from the same origin — threaded through every fetch the UI makes. That is
a server-side feature with its own D-number, not a Java convenience.

## 4. Decision matrix

| Option | Code | New permissions | Link survives app close / screen off | Endorses unauthenticated exposure | Verdict |
|---|---|---|---|---|---|
| **A.** Share button only, no lifetime work | ~40 lines Java | none | no / no | yes | **Reject** — ships a promise it breaks within a minute |
| **B.** A + foreground service + wake/Wi-Fi lock + power pin | ~250 lines Java, 1 new service, manifest + Play declaration | `WAKE_LOCK`, `FOREGROUND_SERVICE_CONNECTED_DEVICE` | yes / yes | yes | **Hold** — honest, but see §3: it is a sharing *mode*, not a button |
| **C.** B + bearer-token auth in Go | B + a server feature | as B | yes / yes | no | **The only complete answer.** Needs its own decision first |
| **D.** Show the address, do not share it | ~20 lines Java | none | n/a | no | **Accept if anything is done now** — see §5 |
| **E.** Do nothing | 0 | none | n/a | no | **Default** |

## 5. Verdict

**No to A, which is what was asked for.** It is the cheapest option and the one
that produces the worst artefact: a URL in someone else's chat history that
stops working when the sender closes the app, cannot be revoked, and needed no
password while it did work.

**No to B on its own.** Once the lifetime problem is solved properly the feature
is not a button, it is a *mode* — "Share on Wi-Fi", with a notification the user
can see and stop, a visible session, and its own row. That is a legitimate
feature and it should be proposed as one, with §3 answered first.

**If something must ship now, ship D:** when *Reachable from* is on, the hint
under that row gains the address the phone is currently reachable at, as text.
No button, no chooser, no promise of lifetime — the user reads it and does
whatever they were going to do. It is the smallest thing that removes the actual
friction (the user has no way to learn their own IP) without the app endorsing
anything. Under D102 it passes: the address is something the user must *act* on,
and the switch is already on, so nothing is being suggested that they have not
already chosen.

## 6. Deferred: discovery

mDNS/NSD (`NsdManager.registerService`, `_http._tcp`, name
`WuWeiDict on <device>`) would remove the IP fragility entirely — the recipient
opens `wuweidict.local:6888`. Costs: multicast is filtered under Wi-Fi power
save (so it needs §2.2's Wi-Fi lock anyway), `.local` resolution is unreliable
on Android clients themselves, and it broadcasts the library's existence to
every device on the network, which is a privacy decision of its own. Only worth
it inside option C.

## 7. If it is ever built — the full checklist

1. **Go:** bearer-token auth (`WUDICT_TOKEN`), middleware over every non-loopback
   request, token excluded from access logs, `/api/config` never echoing it.
   Tests: loopback bypasses, remote without token 401, remote with token 200.
2. **Go:** a `PowerShared` state, or an explicit "pinned" flag, so a remote
   reader is not starved by `PowerBackground`.
3. **Java `LanShare.java`:** address discovery via `NetworkInterface` (§1),
   session start/stop, token generation, URL assembly, `ACTION_SEND` chooser.
4. **Java `ShareService.java`:** foreground service, type `connectedDevice`,
   ongoing notification with a **Stop sharing** action, `PARTIAL_WAKE_LOCK` +
   `WifiLock` acquired for the session and released in `onDestroy`, all of it
   fail-open the way `IndexService` is.
5. **`ServerProcess`:** a share session is a `retain()` holder, so the existing
   refcount keeps the child alive and no new lifetime rule is invented.
6. **Manifest:** two permissions, one service, one notification channel.
7. **Settings screen:** the row becomes a mode with a visible session state;
   the D101 stale/restart footer must account for a running share session
   (stopping the server ends someone else's read).
8. **Play:** foreground-service-type justification, and a privacy-policy line
   about what is exposed.
9. **Docs:** `docs/SPEC.md` auth section; a D-number for the token design;
   `docs/CLIENT-API.md` gains the header.
10. **Manual:** two devices, screen off 10 minutes, phone in doze
    (`adb shell dumpsys deviceidle force-idle`), roam between APs, guest network
    with client isolation, and a revoked token.
