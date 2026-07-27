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
#
# D0: go.mod carries `replace github.com/monstercameron/GoWebComponents/v5 =>
# ../GoWebComponents`, because v5.0.0 was never tagged. Building here therefore
# needs that sibling checkout NEXT TO this one. `make deps` says so loudly rather
# than letting `go build` fail with a path error nobody reads.

SHELL := /bin/bash
.SHELLFLAGS := -eu -o pipefail -c

ROOT     := $(abspath $(dir $(lastword $(MAKEFILE_LIST))))
GWC_SRC  := $(abspath $(ROOT)/../GoWebComponents)
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

.PHONY: help deps gen build test wasm demo run dev e2e lint migrate tools clean \
        linux install-service backup

help:
	@echo 'ArticleFlux task runner (mirrors scripts/make.ps1)'
	@echo
	@echo '  make deps      check the toolchain and the D0 sibling checkout'
	@echo '  make tools     build gwc.exe from ../GoWebComponents (D0)'
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
	@echo '  make clean     remove bin/ (which is all generated output)'
	@echo
	@echo 'Deployment (see deploy/README.md):'
	@echo '  make linux           build the server and client for this machine'
	@echo '  make install-service install the systemd unit and reload'
	@echo '  make backup          verified point-in-time copy into ./backups'

# --- toolchain -----------------------------------------------------------------

deps:
	@command -v go >/dev/null || { \
	  echo 'go is not installed. Ubuntu: see deploy/README.md — the archive from'; \
	  echo 'go.dev/dl, not apt, which ships a version too old for this go.mod.'; exit 1; }
	@go version
	@if [ ! -d "$(GWC_SRC)" ]; then \
	  echo; \
	  echo 'GoWebComponents is not checked out at $(GWC_SRC).'; \
	  echo; \
	  echo 'D0: go.mod replaces the v5 module with a sibling directory because'; \
	  echo 'v5.0.0 was never tagged or published. Until it is, this build needs'; \
	  echo 'the checkout to sit NEXT TO this one:'; \
	  echo; \
	  echo '  cd $(dir $(ROOT)) && git clone <gwc-remote> GoWebComponents'; \
	  echo; \
	  exit 1; \
	fi
	@echo "GoWebComponents: $(GWC_SRC)"

tools: deps
	@mkdir -p $(BIN)
	cd $(GWC_SRC) && go build -o $(BIN)/gwc ./tools/gwc
	@echo "  $(BIN)/gwc"

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
	GOOS=js GOARCH=wasm go build $(WASMFLAGS) -o $(WASM) ./client/app
	@# wasm_exec.js must come from the toolchain that produced the module. A stale
	@# copy from an older Go fails at instantiate with an import mismatch that
	@# reads like a corrupt binary.
	@exec_js="$$(go env GOROOT)/lib/wasm/wasm_exec.js"; \
	 [ -f "$$exec_js" ] || exec_js="$$(go env GOROOT)/misc/wasm/wasm_exec.js"; \
	 [ -f "$$exec_js" ] || { echo "wasm_exec.js not found under $$(go env GOROOT)"; exit 1; }; \
	 cp "$$exec_js" $(OUT)/wasm_exec.js
	@for f in $(WASM) $(OUT)/wasm_exec.js; do \
	  gzip -9 -c "$$f" > "$$f.gz.tmp" && mv -f "$$f.gz.tmp" "$$f.gz"; \
	done
	@# One decimal, matching what make.ps1 prints. Integer MB would report a 26.6 MB
	@# bundle and a 25.9 MB one as the same number, which is most of the resolution
	@# the G5 ratchet is watching for.
	@raw=$$(stat -c%s $(WASM)); gz=$$(stat -c%s $(WASM).gz); \
	 fmt() { echo "$$(( $$1 * 10 / 1048576 ))" | sed 's/\(.\)$$/.\1/;s/^\./0./'; }; \
	 echo "    app.wasm = $$(fmt $$raw) MB raw / $$(fmt $$gz) MB gzipped  (G5 ratchet — plan.md R4)"

# The GitHub Pages demo (client/demo + client/demodata), built exactly the way
# .github/workflows/pages.yml builds it — same flags, same three files, same
# gzip-only module. The deployed demo is the only build of this application that
# strangers see, so a local one that differed from it would be a rehearsal of a
# different performance.
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
	@sed "s|const MODULE = 'app.wasm';|const MODULE = 'app.wasm.gz';|" web/index.html > $(DEMO)/index.html
	@grep -q "const MODULE = 'app.wasm.gz';" $(DEMO)/index.html || { 	  echo "index.html has no 'const MODULE = ...' line to stamp — the demo would 404 on every boot"; 	  exit 1; }
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
	GOOS=js GOARCH=wasm go build -trimpath \
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
