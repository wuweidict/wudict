# wudict — developer UI & manifest (Decision D10)
# Every meaningful action lives here; `make` alone shows this menu.
# New capabilities MUST land with a target.

# One binary, one entry point (D28). Go names an installed binary after the
# last element of its package path, and the module is .../wudict, so the
# module-root main package is all that's needed to get "wudict".
BINARY     := wudict
CMD        := .
BUILD_DIR  := dist

# ---- build flavours -----------------------------------------------------
# wudict builds in two flavours; `build`/`install`/`check` use cgo:
#
#   cgo  (what `make build` does): CGO_ENABLED=1 -tags sqlite_fts5
#     * mattn/go-sqlite3 — fast FTS5 (D4)
#     * built-in libspeex .spx decoder (internal/speex + internal/speex/clib)
#     Requires a C compiler.
#
#   purego (`make build-purego`, `make cross` releases): CGO_ENABLED=0 -tags purego
#     * modernc.org/sqlite — pure Go, no C toolchain
#     * NO built-in speex; .spx audio falls back to the external `speexdec`
#       binary (SPEEX_BACKEND=external has the same effect on a cgo build)
#
# The sqlite_fts5 tag IS the cgo selector (D29): store/driver_cgo.go is
# `sqlite_fts5 && cgo`, so dropping the tag no longer yields a mattn build
# without FTS5 — it yields the pure-Go driver, which always has FTS5. The
# purego flavour's tag is now redundant but kept: it lands in the same place.
# A tag-less `go build`/`go install` therefore always works.
GO_TAGS      := sqlite_fts5
GOFLAGS      := -tags $(GO_TAGS) -trimpath
PUREGO_FLAGS := -tags purego -trimpath
# VERSION must be defined BEFORE LDFLAGS: `:=` expands immediately, so the
# other order stamped the version with an empty string (the binary then printed
# a blank version, overriding cli.Version's "dev" default).
# The stamp targets internal/cli, not main: main is a one-line shim (D28).
VERSION    := $(shell git -C . describe --tags --always --dirty 2>/dev/null || echo dev)
VERSION_PKG := github.com/wuweidict/wudict/internal/cli
LDFLAGS    := -s -w -X $(VERSION_PKG).Version=$(VERSION)

# Integration tests need real dictionaries; point these at files you have
# (tests skip silently when a path is unset/missing).
WUDICT_TEST_MDX      ?= $(HOME)/Downloads/Language/mdict/es-es-Espasa-Calpe-2016.mdx
WUDICT_TEST_STARDICT ?= $(HOME)/Downloads/Language/stardict/eng-eng-stanford-ep.ifo
WUDICT_TEST_SLOB     ?= $(HOME)/Downloads/Language/aard/es-es-Espasa-Calpe-2016.slob
WUDICT_TEST_DSL      ?= $(HOME)/Downloads/Language/DSL/es-es-Espasa-Calpe-2016/es-es-Espasa-Calpe-2016.dsl
TEST_ENV = WUDICT_TEST_MDX="$(WUDICT_TEST_MDX)" WUDICT_TEST_STARDICT="$(WUDICT_TEST_STARDICT)" WUDICT_TEST_SLOB="$(WUDICT_TEST_SLOB)" WUDICT_TEST_DSL="$(WUDICT_TEST_DSL)"

# Args for `make run-*` targets, e.g.: make run ARGS="list ~/Dictionaries"
ARGS ?=

.DEFAULT_GOAL := help

# ---- meta ---------------------------------------------------------------

.PHONY: help
help: ## Show this help
	@echo "wudict make targets:"; echo
	@awk 'BEGIN{FS=":.*##"} /^[a-zA-Z0-9_.-]+:.*##/{printf "  \033[36m%-24s\033[0m %s\n", $$1, $$2}' $(MAKEFILE_LIST)
	@echo
	@echo "Vars: ARGS=<cli args>  WUDICT_TEST_MDX|_STARDICT|_SLOB|_DSL=<integration fixtures>"

# ---- build & run --------------------------------------------------------

.PHONY: build
build: ## Build the host binary — cgo flavour (built-in sqlite FTS5 + built-in speex .spx decoder)
	CGO_ENABLED=1 go build $(GOFLAGS) -ldflags "$(LDFLAGS)" -o $(BINARY) $(CMD)

.PHONY: build-purego
build-purego: ## Build the host binary — purego flavour (*slower* pure-Go sqlite, external speexdec)
	CGO_ENABLED=0 go build $(PUREGO_FLAGS) -ldflags "$(LDFLAGS)" -o $(BINARY) $(CMD)

.PHONY: install
install: ## go install the wudict binary into GOBIN
	go install $(GOFLAGS) -ldflags "$(LDFLAGS)" $(CMD)

.PHONY: run
run: build open ## Build then run with ARGS, e.g. make run ARGS="lookup dict.mdx word"
	./$(BINARY) --verbose $(ARGS)

.PHONY: ingest
ingest: build ## Ingest DICT=<path> into a text.db (OUT=<path> optional), e.g. make ingest DICT=~/Dicts/x.mdx
	@test -n "$(DICT)" || { echo "usage: make ingest DICT=<dictfile> [OUT=<out.db>]"; exit 2; }
	./$(BINARY) ingest $(if $(OUT),-o "$(OUT)") "$(DICT)"

.PHONY: serve
serve: build ## Run the HTTP server (DICT_DIR/PORT/ARGS overridable), e.g. make serve DICT_DIR=~/Dicts
	./$(BINARY) serve $(if $(DICT_DIR),-dict-dir "$(DICT_DIR)") $(if $(PORT),-port $(PORT)) $(ARGS)

.PHONY: cross
cross: ## Cross-compile all release targets into dist/ (purego flavour: pure-Go sqlite, no C toolchain; .spx via external speexdec)
	@mkdir -p $(BUILD_DIR)
	@for target in darwin/arm64 darwin/amd64 linux/amd64 linux/arm64 linux/arm/7 linux/arm/6 windows/amd64 windows/arm64; do \
	  os=$$(echo $$target | cut -d/ -f1); arch=$$(echo $$target | cut -d/ -f2); arm=$$(echo $$target | cut -d/ -f3); \
	  ext=""; [ "$$os" = windows ] && ext=".exe"; \
	  suffix="$$os-$$arch"; [ -n "$$arm" ] && suffix="$$suffix-v$$arm"; \
	  echo "building $$suffix"; \
	  CGO_ENABLED=0 GOOS=$$os GOARCH=$$arch GOARM=$$arm go build -trimpath -tags purego -ldflags "$(LDFLAGS)" \
	    -o $(BUILD_DIR)/$(BINARY)-$$suffix$$ext $(CMD) || exit 1; \
	done

# ---- tray (D74, D76) ----------------------------------------------------
# There is nothing to build here. The tray icon is off unless the app was
# launched from a desktop, and every platform — Windows included, since D76 —
# reads that from the running process rather than from a second artifact.

.PHONY: icons
icons: ## Regenerate the committed tray PNGs + macOS .icns from internal/server/web/favicon.svg (needs rsvg-convert)
	@sh tools/make-icons.sh

# ---- macOS .app bundle (D75) --------------------------------------------
# The bundle is the only macOS launch path that tells the binary a person
# started it: internal/tray looks for ".app/Contents/MacOS/" in its own
# os.Executable(). Nothing else about the product changes — a bare `wudict`
# already serves, and a second launch already finds the port taken, opens the
# browser and exits.
#
# The bundle NAME is derived by tools/make-app.sh from `wudict --version`, so
# it cannot drift from cli.ProductName the way a copy of the name here would.
# The script reports the path it built in $(BUILD_DIR)/.app-path, which is how
# the install target addresses a name Make does not know.
APP_BIN     ?= $(abspath $(BINARY))
APP_ID      ?= com.legbehindneck.wudict
APP_DEST    ?= $(HOME)/Applications
MACOS_MIN   ?= 12.0
CODESIGN_ID ?= -

.PHONY: mac-app
mac-app: build ## Build the double-clickable macOS .app into dist/ (menu-bar icon, no Dock icon)
	@APP_BIN="$(APP_BIN)" APP_ID="$(APP_ID)" MACOS_MIN="$(MACOS_MIN)" BUILD_DIR="$(BUILD_DIR)" \
	  CODESIGN_ID="$(CODESIGN_ID)" sh tools/make-app.sh

.PHONY: mac-app-install
mac-app-install: mac-app ## Install that .app into ~/Applications (per-user, no sudo; override APP_DEST=)
	@app=$$(cat $(BUILD_DIR)/.app-path); name=$$(basename "$$app"); \
	  mkdir -p "$(APP_DEST)"; \
	  rm -rf "$(APP_DEST)/$$name"; \
	  cp -R "$$app" "$(APP_DEST)/$$name"; \
	  echo "installed $(APP_DEST)/$$name"; \
	  echo "next: open '$(APP_DEST)/$$name'"

# ---- Windows installer (D76, P86) -------------------------------------------
# One .exe, installed per-user with no admin prompt. Needs Inno Setup 6, so it
# runs on Windows (CI does it in build-cgo.yml); everywhere else the target
# exists to tell you that, which is D10's point.
WIN_EXE ?= $(abspath $(BINARY)).exe

.PHONY: win-installer
win-installer: ## Compile the Windows per-user installer into dist/ (needs Inno Setup 6)
	@WIN_EXE="$(WIN_EXE)" BUILD_DIR="$(BUILD_DIR)" sh tools/make-installer.sh

# ---- Android (D52, D53) -----------------------------------------------------
# The app is android/: a WebView shell around the same binary, shipped inside
# the APK as libwudict.so, which the shell execs as a child process.
#
# `android-go` is the CGO flavour (D53): mattn FTS5 + built-in speex .spx
# decoder, cross-compiled with the NDK — desktop parity on-device. The
# external linker is given -Wl,-z,max-page-size=16384 (verified: PT_LOAD
# alignment 0x4000), so the 16 KiB page-size mandate is met.
# `android-go-purego` is the NDK-less fallback (slower sqlite, no .spx audio).
#
# NDK resolution: $ANDROID_NDK_HOME, else $ANDROID_NDK, else the newest
# version under the SDK's ndk/ dir ($ANDROID_HOME, then the macOS default).
# ANDROID_API matches the app's minSdk.
ANDROID_SDK  ?= $(or $(ANDROID_HOME),$(HOME)/Library/Android/sdk)
ANDROID_NDK  ?= $(or $(ANDROID_NDK_HOME),$(lastword $(sort $(wildcard $(ANDROID_SDK)/ndk/*))))
NDK_HOST     := $(if $(filter Darwin,$(shell uname)),darwin-x86_64,linux-x86_64)
NDK_BIN      := $(ANDROID_NDK)/toolchains/llvm/prebuilt/$(NDK_HOST)/bin
ANDROID_API  ?= 26
ANDROID_LIB  := android/app/src/main/jniLibs/arm64-v8a/libwudict.so

# Gradle needs JDK 17+. Respect an inherited JAVA_HOME; on macOS fall back to
# an installed JDK 17 rather than whatever ancient default `java` resolves to.
ifeq ($(shell uname),Darwin)
export JAVA_HOME ?= $(shell /usr/libexec/java_home -v 21 2>/dev/null)
endif

.PHONY: android-go
android-go: ## Cross-compile the server into the APK's jniLibs (android/arm64, cgo: fast FTS5 + built-in speex; needs the NDK)
	@test -n "$(ANDROID_NDK)" || { echo "error: no Android NDK found."; \
	  echo "  install one: $(ANDROID_SDK)/cmdline-tools/latest/bin/sdkmanager 'ndk;30.0.15729638'"; \
	  echo "  or set ANDROID_NDK_HOME / ANDROID_NDK"; exit 2; }
	@mkdir -p $(dir $(ANDROID_LIB))
	CC="$(NDK_BIN)/aarch64-linux-android$(ANDROID_API)-clang" \
	CXX="$(NDK_BIN)/aarch64-linux-android$(ANDROID_API)-clang++" \
	CGO_ENABLED=1 GOOS=android GOARCH=arm64 go build $(GOFLAGS) \
	  -ldflags "$(LDFLAGS) -extldflags '-Wl,-z,max-page-size=16384'" -o $(ANDROID_LIB) $(CMD)

.PHONY: android-go-purego
android-go-purego: ## NDK-less fallback lib (pure-Go sqlite; .spx audio unavailable on-device)
	@mkdir -p $(dir $(ANDROID_LIB))
	CGO_ENABLED=0 GOOS=android GOARCH=arm64 go build $(PUREGO_FLAGS) -ldflags "$(LDFLAGS)" -o $(ANDROID_LIB) $(CMD)

# Two flavours (D62), one binary: `foss` is the GitHub/F-Droid build and keeps
# All-files access; `play` declares no storage permission and imports through
# SAF. Both package the same libwudict.so, so android-go is a shared prereq.
.PHONY: apk
apk: android-go ## Build the FOSS debug APK (needs Android SDK: ANDROID_HOME or local.properties)
	cd android && ./gradlew assembleFossDebug
	@echo "android/app/build/outputs/apk/foss/debug/$(BINARY)-foss-debug.apk"

.PHONY: apk-foss-release
apk-foss-release: android-go ## Build the FOSS release APK (signed only if a keystore is exported — see build-android.yml)
	cd android && ./gradlew assembleFossRelease
	@# The APK is left where Gradle put it, deliberately: a copy under dist/
	@# survives a FAILED build, so `adb install dist/wudict.apk` would then
	@# silently install the previous one — exactly when you are debugging and
	@# least likely to notice. archivesName already gives it a real name; the
	@# echo is only so you do not have to remember the path.
	@echo "android/app/build/outputs/apk/foss/release/$(BINARY)-foss-release.apk"

.PHONY: apk-foss-release-install
apk-foss-release-install: apk-foss-release ## build FOSS release and install via adb
	adb install "android/app/build/outputs/apk/foss/release/$(BINARY)-foss-release.apk"

.PHONY: apk-play-debug
apk-play-debug: android-go ## Build the Play-flavour debug APK (SAF import)
	cd android && ./gradlew assemblePlayDebug
	@echo "android/app/build/outputs/apk/play/debug/$(BINARY)-play-debug.apk"

.PHONY: apk-play-release
apk-play-release: android-go ## Build the Play-flavour release APK (SAF import)
	cd android && ./gradlew assemblePlayRelease
	@echo "android/app/build/outputs/apk/play/release/$(BINARY)-play-release.apk"

.PHONY: apk-play-release-install
apk-play-release-install: apk-play-release ## build FOSS release and install via adb
	adb install "android/app/build/outputs/apk/foss/release/$(BINARY)-foss-release.apk"

.PHONY: aab-play
aab-play: android-go ## Build the Play release bundle (unsigned: Play App Signing owns the key)
	cd android && ./gradlew bundlePlayRelease
	@echo "android/app/build/outputs/bundle/playRelease/$(BINARY)-play-release.aab"

.PHONY: apk-verify
apk-verify: ## Assert the Play APK declares no storage permission and still extracts the binary
	@sh tools/verify-apk.sh

.PHONY: test-purego
test-purego: ## Run store tests against the pure-Go sqlite driver (release parity)
	CGO_ENABLED=0 go test -tags purego ./internal/store/ ./internal/server/

# ---- quality ------------------------------------------------------------

.PHONY: test
test: ## Unit tests (integration tests skip unless WUDICT_TEST_MDX exists)
	$(TEST_ENV) go test $(GOFLAGS) ./...

.PHONY: test-v
test-v: ## Tests, verbose
	$(TEST_ENV) go test $(GOFLAGS) -v ./...

.PHONY: cover
cover: ## Tests with coverage report
	$(TEST_ENV) go test $(GOFLAGS) -coverprofile=coverage.out ./...
	go tool cover -func=coverage.out | tail -1

.PHONY: bench
bench: ## Run benchmarks
	$(TEST_ENV) go test $(GOFLAGS) -bench=. -benchmem -run=^$$ ./...

.PHONY: vet
vet: ## go vet
	go vet $(GOFLAGS) ./...

.PHONY: fmt
fmt: ## gofmt all sources in place
	gofmt -w $$(find . -name '*.go' -not -path './dist/*')

.PHONY: lint
lint: vet ## golangci-lint if installed, else vet only
	@command -v golangci-lint >/dev/null && golangci-lint run --build-tags $(GO_TAGS) ./... || echo "golangci-lint not installed; ran vet only"

.PHONY: tidy
tidy: ## go mod tidy
	go mod tidy

.PHONY: check
check: tidy vet test ## Pre-commit gate: tidy + vet + test

# ---- launchd agent (macOS) ----------------------------------------------
# Modern launchctl syntax (bootstrap/bootout/kickstart), GUI domain.
#
# The plist is GENERATED, never committed: launchd expands nothing in
# ProgramArguments (no ~, no $HOME, no PATH lookup), so argv[0] must be a
# literal absolute path — a checked-in plist is only correct on the machine
# that wrote it. launchctl/*.plist.in is the template.

LABEL    := com.legbehindneck.wudict
PLIST_IN := launchctl/$(LABEL).plist.in
PLIST    := $(HOME)/Library/LaunchAgents/$(LABEL).plist
GUI      := gui/$(shell id -u)
LOGDIR   := $(HOME)/Library/Logs
# Which binary the agent runs. Defaults to THIS checkout's build, so that
# `make mac-agent-restart` (rebuild + kickstart) is coherent. Point it at the
# installed copy to survive moving/renaming the checkout:
#   make mac-agent-install AGENT_BIN="$$(go env GOPATH)/bin/wudict"
AGENT_BIN ?= $(abspath $(BINARY))

.PHONY: mac-agent-install
mac-agent-install: build ## Generate + install the LaunchAgent plist (override AGENT_BIN=<path>)
	@mkdir -p $(dir $(PLIST)) $(LOGDIR)
	@sed -e '/<!-- TEMPLATE/,/^-->$$/d' \
	     -e 's|@LABEL@|$(LABEL)|g' -e 's|@BIN@|$(AGENT_BIN)|g' -e 's|@LOGDIR@|$(LOGDIR)|g' \
	   $(PLIST_IN) > $(PLIST)
	@plutil -lint $(PLIST) >/dev/null || { rm -f $(PLIST); echo "generated plist is invalid"; exit 1; }
	@echo "installed $(PLIST)"
	@echo "       -> $(AGENT_BIN) serve --no-browser"
	@echo "       -> log: $(LOGDIR)/wudict.log"
	@echo "next: make mac-agent-start"

.PHONY: mac-agent-uninstall
mac-agent-uninstall: ## Stop the agent and remove its plist
	-launchctl bootout $(GUI)/$(LABEL) 2>/dev/null
	rm -f $(PLIST)

.PHONY: mac-agent-start
mac-agent-start: ## Load and start the launchd agent
	@test -f $(PLIST) || { echo "no plist at $(PLIST) — run: make mac-agent-install"; exit 2; }
	launchctl bootstrap $(GUI) $(PLIST)

.PHONY: mac-agent-stop
mac-agent-stop: ## Stop and unload the launchd agent
	launchctl bootout $(GUI)/$(LABEL)

.PHONY: mac-agent-restart
mac-agent-restart: build ## Rebuild the binary, then restart the running agent
	launchctl kickstart -k $(GUI)/$(LABEL)

.PHONY: mac-agent-status
mac-agent-status: ## Show the agent's launchd state (pid, exit status, ...)
	launchctl print $(GUI)/$(LABEL)

# ---- systemd service (Linux) --------------------------------------------
# A USER unit (systemctl --user): WuWeiDict reads ~/Dictionaries, writes
# ~/.wudict and binds 127.0.0.1, so it belongs to the user's session, not to
# root. Only copying the binary into $(PREFIX)/bin needs sudo.
#
# The unit file *must* be generated — ExecStart must be an absolute
# path and systemd doesn't do ~ expansion.

PREFIX      ?= /usr/local
SERVICE_BIN := $(PREFIX)/bin/$(BINARY)
UNIT_NAME   := wudict.service
UNIT_IN     := systemd/$(UNIT_NAME).in
UNIT_DIR    := $(if $(XDG_CONFIG_HOME),$(XDG_CONFIG_HOME),$(HOME)/.config)/systemd/user
UNIT        := $(UNIT_DIR)/$(UNIT_NAME)

.PHONY: linux-install
linux-install: build ## Install the binary into $(PREFIX)/bin (asks for sudo)
	sudo install -D -m 0755 $(BINARY) $(SERVICE_BIN)
	@echo "installed $(SERVICE_BIN)"

.PHONY: linux-uninstall
linux-uninstall: ## Remove the binary from $(PREFIX)/bin (asks for sudo)
	sudo rm -f $(SERVICE_BIN)

.PHONY: linux-service-install
linux-service-install: linux-install ## Install binary + generate the systemd user unit
	@mkdir -p $(UNIT_DIR)
	@sed -e '/^#!/d' -e 's|@BIN@|$(SERVICE_BIN)|g' $(UNIT_IN) > $(UNIT)
	systemctl --user daemon-reload
	@echo "installed $(UNIT)"
	@echo "       -> $(SERVICE_BIN) serve --no-browser"
	@echo "next: make linux-service-start   (and 'sudo loginctl enable-linger $$(id -un)' to run without a login session)"

.PHONY: linux-service-uninstall
linux-service-uninstall: ## Stop/disable the service and remove its unit (keeps the binary)
	-systemctl --user disable --now $(UNIT_NAME)
	rm -f $(UNIT)
	systemctl --user daemon-reload

.PHONY: linux-service-start
linux-service-start: ## Enable and start the service now
	@test -f $(UNIT) || { echo "no unit at $(UNIT) — run: make linux-service-install"; exit 2; }
	systemctl --user enable --now $(UNIT_NAME)

.PHONY: linux-service-stop
linux-service-stop: ## Stop the service
	systemctl --user stop $(UNIT_NAME)

.PHONY: linux-service-restart
linux-service-restart: linux-install ## Rebuild, reinstall the binary, then restart the service
	systemctl --user restart $(UNIT_NAME)

.PHONY: linux-service-status
linux-service-status: ## Show service state (add 'journalctl --user -u wudict -f' for logs)
	systemctl --user status $(UNIT_NAME) --no-pager

# ---- housekeeping -------------------------------------------------------

.PHONY: clean
clean: ## Remove binaries, dist/, coverage artifacts
	rm -rf $(BINARY) $(BUILD_DIR) coverage.out

.PHONY: purge
purge: ## zap all local cached dictionaries in ~/.wudict/db
	rm -rfv ~/.wudict/db

.PHONY: version
version: ## Print the version stamp used for builds
	@echo $(VERSION)


.PHONY: remotes
remotes: ## git remotes
	git remote -v
	echo '---'
	git --no-pager branch -vv

.PHONY: open
open: build  ## run browser
	open http://localhost:6888
