# ArticleFlux — the Linux half of the task runner.
#
# TODO 1.4 says "make.ps1, not a Makefile", and gives the reason: there is no
# `make` on the development box, and a Makefile nobody there can run next to the
# script everyone actually runs is two build systems one of which is a lie.
#
# That reasoning was about the DEVELOPMENT box. It does not extend to the
# deployment target: an Ubuntu droplet has make, does not have PowerShell, and
# has to be able to build and run this. 1.4 anticipated exactly that — "verb
# names are kept identical so a Makefile stays cheap to add if it ever earns its
# place" — and A9 (remote deployment) is where it earns it.
#
# So the rule is: THE VERBS MUST MATCH scripts/make.ps1, one for one. Two build
# systems is only a lie if they disagree. Anything added to one goes in the
# other, and `make help` and `make.ps1 help` list the same set.

SHELL := /bin/bash
.SHELLFLAGS := -eu -o pipefail -c

ROOT     := $(abspath $(dir $(lastword $(MAKEFILE_LIST))))
BIN      := $(ROOT)/bin
OUT      := $(BIN)/web
WASM     := $(OUT)/app.wasm
DEMO     := $(BIN)/demo
PORT     ?= 9000
DB       ?= articleflux.db
# What a `demo` build calls itself. The release workflow passes the tag it was
# triggered by; a build from a working copy is honestly "dev".
VERSION  ?= dev

# -trimpath and -ldflags="-s -w" are not decoration: wasm-baseline.txt was
# measured with exactly these, so building differently would make the CI size
# ratchet compare two different artifacts.
#
# ONE -ldflags, carrying both. `go build` parses this flag with StringsFlag,
# whose Set REPLACES the previous value rather than appending to it — so the
# earlier `-ldflags=-s -ldflags=-w` spelling passed only `-w`, and this target
# was quietly measuring a different binary from the one CI and make.ps1 measure.
WASMFLAGS := -trimpath '-ldflags=-s -w'

# GOEXPERIMENT=nojsonv2 on every wasm ARTIFACT build, and only there.
#
# Go 1.27 turned the encoding/json v2 implementation on by default, and linking
# it costs the bundle about a megabyte: 34,510,320 bytes with it against
# 33,455,770 without, measured on this tree on 2026-11-19. Nothing in the client
# asks for v2 (it never imports encoding/json/v2), so that megabyte was the price
# of a toolchain upgrade and bought nothing. GoWebComponents' own launcher forces
# the same setting for the same reason. With it on, the bundle sat four
# kilobytes under the +5% ratchet ceiling — the next feature would have had to
# move wasm-baseline.txt to pay for a JSON implementation nobody uses.
#
# It has to be set in EVERY place that builds the shipped module (this file,
# scripts/make.ps1, ci.yml's ratchet and e2e jobs, deploy/install.sh), or the
# ratchet measures a different binary from the one that ships. The compile-only
# checks (`go build ./client/...`, vet, the wasm tests) do not set it: they
# produce no artifact, and they should keep proving the default toolchain works.
WASMENV := GOEXPERIMENT=nojsonv2 GOOS=js GOARCH=wasm

.PHONY: help deps gen build test wasmtest wasm demo run dev e2e lint migrate perf perf-compare \
        clean linux install-service backup

help:
	@echo 'ArticleFlux task runner (mirrors scripts/make.ps1)'
	@echo
	@echo '  make deps      check that a Go toolchain is installed'
	@echo '  make wasmtest  go test client/data + client/i18n + client/platform + client/view (wasm)'
	@echo '  make gen       buf lint + buf generate -> internal/pb'
	@echo '  make build     go build -> bin/articleflux'
	@echo '  make test      go test ./...'
	@echo '  make wasm      build the client into bin/web, prints the G5 size'
	@echo '  make demo      build the GitHub Pages demo into bin/demo (VERSION=v1.0.0)'
	@echo '  make lint      go vet + buf lint + the A26/tenancy structural guards'
	@echo '  make migrate   apply migrations'
	@echo '  make run       build, then serve on 127.0.0.1:$(PORT)'
	@echo '  make dev       wasm + run, with -dev (NO LOGIN; loopback only)'
	@echo '  make e2e       build, then run the Playwright suite'
	@echo '  make perf      benchmark every package that has one, with CPU/heap profiles'
	@echo '                 LABEL=baseline BENCH=. COUNT=6'
	@echo '                 ends with a verdict on whether its own timings can be trusted'
	@echo '  make perf-compare FROM=baseline TO=after   benchstat two runs'
	@echo '  make clean     remove bin/ (which is all generated output)'
	@echo
	@echo 'Deployment (see deploy/README.md):'
	@echo '  make linux           build the server and client for this machine'
	@echo '  make install-service install the systemd unit and reload'
	@echo '  make backup          verified point-in-time copy into ./backups'

# --- toolchain -----------------------------------------------------------------

# `deps` used to check two things, and the second is gone. go.mod once resolved
# GoWebComponents through a `replace` to a sibling directory, so every build here
# needed that checkout NEXT TO this one, and this target said so rather than
# letting `go build` fail with a path error. GWC is a published module now and
# is downloaded like every other requirement, so nothing has to sit beside this
# repository. What is left is the failure that still deserves its own sentence:
# no Go at all, on a distribution whose obvious fix (apt) installs one too old.
#
# There was a `tools` target here as well, which built the `gwc` launcher from
# that same checkout. Nothing in this file ever ran the result — `wasm` calls
# `go build` directly so its flags stay visible — so it went with the sibling.
deps:
	@command -v go >/dev/null || { \
	  echo 'go is not installed. Ubuntu: see deploy/README.md — the archive from'; \
	  echo 'go.dev/dl, not apt, which ships a version too old for this go.mod.'; exit 1; }
	@go version

gen:
	buf lint
	buf generate

# --- build ---------------------------------------------------------------------

build: deps
	@mkdir -p $(BIN)
	go build -o $(BIN)/articleflux ./cmd/articleflux
	go build ./...
# `go build ./...` does NOT compile the client (8b.32): the wasm packages are
# behind `//go:build js` and a native build skips them, so the client was
# broken for a stretch while every native build and test stayed green.
	GOOS=js GOARCH=wasm go build ./client/...
	@echo "  $(BIN)/articleflux"

test:
	go test ./...

# wasmtest is the ONLY way client/view ever gets tested: it is entirely
# `//go:build js && wasm` (zero untagged files), so `go test ./client/view`
# under a native build fails outright with "build constraints exclude all Go
# files". Cross-compiling the test binary to wasm and running it under Node
# (via `go test -exec`) is what client/i18n/provider_test.go already proves
# works; this is that same harness, made a verb instead of a remembered
# incantation.
#
# GOROOT and the node wrapper are resolved at run time — not hardcoded — so
# this works on a fresh checkout without anyone hunting for wasm_exec_node.js
# under whatever GOROOT this machine's Go toolchain manager happens to use
# (module-cached toolchains put it somewhere under GOPATH, not under a fixed
# system path).
#
# Scoped to named packages rather than ./client/..., because these are the
# ones the harness exists for: i18n is its own proof of life, view is the
# reason it was built, and data and platform are wasm-only packages whose
# tests nothing else can compile. The rest of client/... is native-portable
# (client/design, client/demodata) and is covered by `make test`; dragging it
# through -exec would only surface OS-specific noise.
#
# THE PACKAGE LIST MUST MATCH make.ps1's Invoke-WasmTest AND ci.yml's wasmtest
# job. It did not, and the drift ran the way drift always does — quietly, in
# the direction of less checking. This target named i18n and view only, make.ps1
# named those plus data, and ci.yml named all four; so client/platform's tests
# ran in exactly one of the three places a developer might run them. That is
# the package where LocalNow read the browser's clock through a guard sized for
# media durations, returned zero every time, and greeted every listener with the
# SERVER's clock — a bug that lived precisely because an ungated wasm package is
# a package with no tests at all.
#
# The shim's path is quoted INSIDE -exec, and that is not belt-and-braces.
# `go test` splits the flag's value on spaces and honours quotes around a field,
# so a GOROOT with a space in it — `C:\Program Files\Go`, when this recipe's
# body is run under Git Bash on the development box — otherwise asks node to
# run a file called `C:\Program`, which fails as a missing module rather than as
# a quoting mistake.
wasmtest:
	@exec_js="$$(go env GOROOT)/lib/wasm/wasm_exec_node.js"; \
	 [ -f "$$exec_js" ] || { echo "wasm_exec_node.js not found under $$(go env GOROOT)/lib/wasm — is this Go's Node exec shim missing?"; exit 1; }; \
	 command -v node >/dev/null || { echo "node is not on PATH — the wasm test harness runs the compiled test binary under it"; exit 1; }; \
	 GOOS=js GOARCH=wasm go test -exec="node '$$exec_js'" ./client/data/... ./client/i18n/... ./client/platform/... ./client/view/...

lint:
	go vet ./...
	buf lint
	go run ./internal/tools/guards .

# The client. Mirrors Invoke-Wasm in make.ps1 including the precompressed
# siblings, which are not optional: the server prefers app.wasm.gz when the
# client accepts gzip, so a MISSING .gz is fine and a STALE one silently serves
# the previous build. Hence: compress to a temp file, then move into place. A
# truncated .gz left behind by a failed compressor looks exactly like "my change
# did nothing", and costs an hour every time.
wasm: deps
	@if [ ! -d client/app ]; then \
	  echo '    client/app has no main yet (Tier 8) — skipping wasm'; exit 0; fi
	@mkdir -p $(OUT)
	cp web/index.html $(OUT)/index.html
# index.html registers sw.js by relative path, so a build that does not ship it
# 404s on every load and the offline shell silently never exists (8.4).
	cp web/sw.js $(OUT)/sw.js
# The self-hosted webfonts. index.html links fonts.css by relative path, and
# fonts.css links each woff2 the same way, so missing either leaves the app
# rendering in Georgia and system-ui with nothing in the console to explain it.
# They ship together or the typography silently is not the design.
	cp web/fonts.css $(OUT)/fonts.css
	rm -rf $(OUT)/fonts && cp -r web/fonts $(OUT)/fonts
# The broadcast's music beds (§19), as make.ps1 stages them. Twelve megabytes, fetched
# only when a reader turns a bed on, so a build without them still boots — which is
# exactly why this went missing from this target unnoticed: the container image is built
# with `make wasm`, and shipped without them, and nothing failed. The server looks for
# them at <web root>/audio (internal/app audioDirOf).
	@if [ -d web/audio ]; then rm -rf $(OUT)/audio && cp -r web/audio $(OUT)/audio; fi
# The landing page served at /welcome (internal/app serves <web root>/welcome.html).
# install.sh and update.sh get it for free by copying all of web/; this target and
# make.ps1 copied files by name and so never did, which left /welcome a 404 in the
# container image and in every local build.
	@if [ -f web/welcome.html ]; then cp web/welcome.html $(OUT)/welcome.html; fi
# The manifest and the icons (§20.24). index.html links the manifest by relative
# path and the manifest names each icon the same way, so a build that ships one
# without the other is an app the browser refuses to install — with no error on
# screen, because nothing on the page is broken.
	cp web/manifest.webmanifest $(OUT)/manifest.webmanifest
	rm -rf $(OUT)/icons && cp -r web/icons $(OUT)/icons
# The homepage's screenshots (client/view/home.go), regenerated by
# `node e2e/home-shots.mjs`. Not part of the app shell: a deployment without them
# loses the front door's illustrations rather than the reader.
	rm -rf $(OUT)/shots && cp -r web/shots $(OUT)/shots
	$(WASMENV) go build $(WASMFLAGS) -o $(WASM) ./client/app
	@# wasm_exec.js must come from the toolchain that produced the module. A stale
	@# copy from an older Go fails at instantiate with an import mismatch that
	@# reads like a corrupt binary.
	@exec_js="$$(go env GOROOT)/lib/wasm/wasm_exec.js"; \
	 [ -f "$$exec_js" ] || exec_js="$$(go env GOROOT)/misc/wasm/wasm_exec.js"; \
	 [ -f "$$exec_js" ] || { echo "wasm_exec.js not found under $$(go env GOROOT)"; exit 1; }; \
	 cp "$$exec_js" $(OUT)/wasm_exec.js
	@# Precompressed siblings, both encodings, for everything in the web root
	@# worth compressing — not the two files this loop used to name by hand. See
	@# cmd/precompress for why it is a tool: this rule and make.ps1's copy of it
	@# were two implementations of one decision, and both covered app.wasm and
	@# wasm_exec.js only, which is how index.html shipped uncompressed to
	@# production for the life of the deployment.
	go run ./cmd/precompress $(OUT)
	@# One decimal, matching what make.ps1 prints. Integer MB would report a 26.6 MB
	@# bundle and a 25.9 MB one as the same number, which is most of the resolution
	@# the G5 ratchet is watching for.
	@raw=$$(stat -c%s $(WASM)); gz=$$(stat -c%s $(WASM).gz); \
	 fmt() { echo "$$(( $$1 * 10 / 1048576 ))" | sed 's/\(.\)$$/.\1/;s/^\./0./'; }; \
	 echo "    app.wasm = $$(fmt $$raw) MB raw / $$(fmt $$gz) MB gzipped  (G5 ratchet — plan.md R4)"

# The GitHub Pages demo (client/demo + client/demodata), built exactly the way
# .github/workflows/pages.yml builds it — same flags, same static asset bundle,
# same gzip-only module. The deployed demo is the only build of this application
# that strangers see, so a local one that differed from it would be a rehearsal
# of a different performance.
#
# The raw module is deleted after compressing, and that is the one place this
# differs from `wasm`: a static host cannot negotiate an encoding, so the boot
# shim fetches app.wasm.gz and decompresses it itself (web/index.html). Leaving
# app.wasm beside it would mean that path was never taken locally.
demo: deps
	@mkdir -p $(DEMO)
	@# index.html, with the module name STAMPED. This build publishes only the
	@# gzip (see below), and an unstamped loader asks for app.wasm first — a 404
	@# in the console of every stranger who opens the demo, on every load, for a
	@# file that is deliberately not here. Same idea as the sw.js stamp under it.
	@# The <base href> is stamped in the same pass, and for a related reason.
	@# The served app declares `/` because it answers at every route (§20.13b), so
	@# a relative asset URL on a deep link would resolve under that route. A static
	@# host has no SPA fallback and therefore no deep links, but it DOES publish
	@# under a project-page prefix — so `/` would send every asset request to the
	@# wrong origin root, which is the failure the relative manifest paths below
	@# were already written to avoid.
	@sed -e "s|const MODULE = 'app.wasm';|const MODULE = 'app.wasm.gz';|" \
	     -e 's|<base href="/">|<base href="./">|' web/index.html > $(DEMO)/index.html
	@grep -q "const MODULE = 'app.wasm.gz';" $(DEMO)/index.html || { 	  echo "index.html has no 'const MODULE = ...' line to stamp — the demo would 404 on every boot"; 	  exit 1; }
	@grep -q '<base href="./">' $(DEMO)/index.html || { \
	  echo 'index.html has no `<base href="/">` line to stamp — the demo would resolve every asset against the origin root, not the project page'; \
	  exit 1; }
	@# sw.js, with its cache identity STAMPED — the one file the demo does not
	@# ship verbatim, and the reason is a failure that is invisible for weeks.
	@#
	@# The Service Worker keys its cache on VERSION and serves the wasm module
	@# cache-first, because within a build that URL's contents never change. On
	@# the server that is right: VERSION is buildver.Version, a release changes
	@# the constant, and `activate` drops every older cache. The demo is
	@# published from a TAG, and a tag does not change buildver — so a second
	@# demo release under an unchanged constant would leave every returning
	@# visitor on the module they cached the first time. web/sw.js itself is
	@# untouched, so internal/buildver's test still pins the source to the
	@# constant.
	@sed "s|^const VERSION = .*|const VERSION = '$(VERSION)';|" web/sw.js > $(DEMO)/sw.js
	@grep -q "const VERSION = '$(VERSION)';" $(DEMO)/sw.js || { \
	  echo "sw.js has no 'const VERSION = ...' line to stamp — the demo would ship a stale cache key"; \
	  exit 1; }
	@# The manifest and icons, verbatim (§20.24). They need no stamping: every
	@# path in the manifest is relative, so the same file describes an app at the
	@# root and one at /ArticleFlux/ — which is the whole reason they are relative.
	@cp web/manifest.webmanifest $(DEMO)/manifest.webmanifest
	@rm -rf $(DEMO)/icons && cp -r web/icons $(DEMO)/icons
	@# Self-hosted fonts, for the same reason as the normal wasm target: the
	@# stamped index still links fonts.css, and every URL in it is relative.
	@cp web/fonts.css $(DEMO)/fonts.css
	@rm -rf $(DEMO)/fonts && cp -r web/fonts $(DEMO)/fonts
	$(WASMENV) go build -trimpath \
	  '-ldflags=-s -w -X main.version=$(VERSION)' -o $(DEMO)/app.wasm ./client/demo
	@exec_js="$$(go env GOROOT)/lib/wasm/wasm_exec.js"; \
	 [ -f "$$exec_js" ] || exec_js="$$(go env GOROOT)/misc/wasm/wasm_exec.js"; \
	 [ -f "$$exec_js" ] || { echo "wasm_exec.js not found under $$(go env GOROOT)"; exit 1; }; \
	 cp "$$exec_js" $(DEMO)/wasm_exec.js
	@gzip -9 -c $(DEMO)/app.wasm > $(DEMO)/app.wasm.gz.tmp && mv -f $(DEMO)/app.wasm.gz.tmp $(DEMO)/app.wasm.gz
	@raw=$$(stat -c%s $(DEMO)/app.wasm); gz=$$(stat -c%s $(DEMO)/app.wasm.gz); \
	 fmt() { echo "$$(( $$1 * 10 / 1048576 ))" | sed 's/\(.\)$$/.\1/;s/^\./0./'; }; \
	 echo "    demo.wasm = $$(fmt $$raw) MB raw / $$(fmt $$gz) MB shipped (gzipped)"
	@rm -f $(DEMO)/app.wasm
	@echo '    serve bin/demo with any static file server to look at it'

migrate: build
	$(BIN)/articleflux migrate -db $(DB)

# 127.0.0.1 and no -dev: `run` is a server that requires a login, which is what
# every deployment is. `dev` below is the one that does not, and it says so.
run: build
	$(BIN)/articleflux serve -addr 127.0.0.1:$(PORT) -db $(DB) -web $(OUT)

dev: wasm build
	@echo '*** -dev serves the local account with NO LOGIN. Loopback only. ***'
	@echo '*** Credentials, if you start without -dev to test the login screen: ***'
	@echo '***   see .env.example (default cam / articleflux)                   ***'
	$(BIN)/articleflux serve -addr 127.0.0.1:$(PORT) -db $(DB) -web $(OUT) -dev

e2e: build wasm
	cd e2e && { [ -d node_modules ] || npm install; } && npx playwright test

# perf — the mirror of Invoke-Perf in make.ps1, where the reasoning for every
# flag is written down. The short version: -run '^$' because the store's TESTS
# build a 50,000-row fixture, -benchmem because allocation is half the answer,
# -count because a single sample on a thermally-limited box is a coin flip, and
# a loop over packages because `go test` refuses -cpuprofile across more than one.
#
# Run it on a QUIET machine. A capture taken alongside another `go test` produced
# a ten-fold spread on one unchanged query, and -count does not rescue that:
# contention and thermal drift push one direction for as long as they last, so
# every sample agrees and every sample is wrong. The tell is B/op and allocs/op
# holding steady while ns/op swings — allocation does not care how hot the box is.
#
#	make perf                          -> bin/perf/baseline.txt + profiles
#	make perf LABEL=after              -> bin/perf/after.txt
#	make perf-compare FROM=baseline TO=after
LABEL ?= baseline
BENCH ?= .
COUNT ?= 6
FROM  ?= baseline
TO    ?= after
PERFDIR := $(BIN)/perf

perf:
	@mkdir -p $(PERFDIR)/prof
	@: > $(PERFDIR)/$(LABEL).txt
	@for pkg in $$(go list ./...); do \
	  dir=$$(go list -f '{{.Dir}}' $$pkg); \
	  grep -lq '^func Benchmark' $$dir/*_test.go 2>/dev/null || continue; \
	  short=$$(basename $$pkg); \
	  echo "==> bench $$pkg"; \
	  go test $$pkg -run '^$$' -bench '$(BENCH)' -benchmem -count $(COUNT) \
	    -cpuprofile $(PERFDIR)/prof/$$short.cpu.pprof \
	    -memprofile $(PERFDIR)/prof/$$short.mem.pprof \
	    -o $(PERFDIR)/prof/$$short.test 2>&1 | tee -a $(PERFDIR)/$(LABEL).txt; \
	done
	@echo "==> wrote $(PERFDIR)/$(LABEL).txt; profiles in $(PERFDIR)/prof"
	@echo
	@go run ./internal/tools/benchspread $(PERFDIR)/$(LABEL).txt

perf-compare:
	@command -v benchstat >/dev/null || { echo 'benchstat not on PATH: go install golang.org/x/perf/cmd/benchstat@latest'; exit 1; }
	@test -f $(PERFDIR)/$(FROM).txt || { echo "$(PERFDIR)/$(FROM).txt does not exist; run: make perf LABEL=$(FROM)"; exit 1; }
	@test -f $(PERFDIR)/$(TO).txt   || { echo "$(PERFDIR)/$(TO).txt does not exist; run: make perf LABEL=$(TO)"; exit 1; }
	benchstat '$(FROM)=$(PERFDIR)/$(FROM).txt' '$(TO)=$(PERFDIR)/$(TO).txt'

clean:
	rm -rf $(BIN)

# --- deployment ----------------------------------------------------------------

# Everything a droplet needs, in one verb. Separate from `build` because it also
# builds the client, and a server-only rebuild during development should not pay
# for a 26 MB wasm compile.
linux: build wasm
	@echo
	@echo 'Built. Next:'
	@echo '  sudo make install-service   (first time only)'
	@echo '  sudo systemctl restart articleflux'

install-service:
	@[ "$$(id -u)" = 0 ] || { echo 'install-service needs root (sudo make install-service)'; exit 1; }
	install -m 0644 deploy/articleflux.service /etc/systemd/system/articleflux.service
	systemctl daemon-reload
	@echo 'Installed. Edit /etc/systemd/system/articleflux.service if your paths differ,'
	@echo 'then: systemctl enable --now articleflux'

backup: build
	$(BIN)/articleflux backup -db $(DB) -out backups/ -keep 14
