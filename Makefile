#   make build      daemon + UI into bin/
#   make run-mock   UI against the mock daemon
#   make check      vet, test, build, shellcheck, smoke and UI tests
#   make chaos      random UI actions against the mock, checking invariants
#                   (SEED=, default random; STEPS=, default 2000)
#   make perf       scripts/perf.sh -> build/perf.txt
#   make install    to ~/.local (PREFIX=...) with WhatsApp's icon and sound;
#                   the systemd user unit is not enabled
#   make autostart  install, then start wispd at login, in the tray (no-autostart undoes it)
SHELL := /bin/bash
.NOTPARALLEL:
.SHELLFLAGS := -o pipefail -c
ROOT := $(abspath .)
BIN := $(ROOT)/bin
UI_BUILD := $(ROOT)/build/ui
UI_EXE := $(UI_BUILD)/wisp
ifeq ($(shell uname -s),Darwin)
UI_EXE := $(UI_BUILD)/wisp.app/Contents/MacOS/wisp
endif
GO ?= go
VERSION := $(shell cat VERSION)
BUILD_TYPE ?= RelWithDebInfo
UI_TARGETS ?= all

PREFIX ?= $(HOME)/.local
DESTDIR ?=
BINDIR := $(DESTDIR)$(PREFIX)/bin
APPDIR := $(DESTDIR)$(PREFIX)/share/applications
ICONDIR := $(DESTDIR)$(PREFIX)/share/icons/hicolor
DATADIR := $(DESTDIR)$(PREFIX)/share/wisp
UNITDIR := $(DESTDIR)$(PREFIX)/share/systemd/user
WHATSAPP := $(ROOT)/build/whatsapp
# A git worktree uses the main checkout's asset cache when it has none of its own.
ifeq ($(wildcard $(WHATSAPP)),)
MAIN_WHATSAPP := $(abspath $(shell git rev-parse --git-common-dir 2>/dev/null)/../build/whatsapp)
WHATSAPP := $(if $(wildcard $(MAIN_WHATSAPP)),$(MAIN_WHATSAPP),$(WHATSAPP))
endif

# quiet-qml-import.cmake skips Qt's static QML import scan, which with a shared
# Qt only prints "will not be linked" warnings.
UI_CMAKE_FLAGS := -G Ninja -DCMAKE_BUILD_TYPE=$(BUILD_TYPE) -Wno-dev \
	-DCMAKE_PROJECT_INCLUDE=$(ROOT)/scripts/cmake/quiet-qml-import.cmake \
	-DWISP_ASSET_CACHE=$(WHATSAPP)

.PHONY: all build daemon ui run run-mock check chaos test vet lint smoke perf install uninstall autostart no-autostart clean

all: build

build: daemon ui

daemon:
	@mkdir -p $(BIN)
	cd daemon && $(GO) build -p 2 -trimpath -ldflags="-s -w -X wisp/daemon/internal/version.Version=$(VERSION)" -o $(BIN)/ ./cmd/...

ui:
	@cmake --log-level=WARNING -S ui -B $(UI_BUILD) $(UI_CMAKE_FLAGS)
	cmake --build $(UI_BUILD) --parallel 2 --target $(UI_TARGETS)
	@mkdir -p $(BIN) && ln -sfn $(UI_EXE) $(BIN)/wisp

run: build
	$(BIN)/wisp

run-mock: build
	$(BIN)/wisp --mock --daemon $(BIN)/wispd

vet:
	cd daemon && $(GO) vet -p 2 ./...

test:
	cd daemon && $(GO) test -p 1 -parallel 1 ./...

lint:
	shellcheck scripts/*.sh

smoke: build
	scripts/smoke.sh

check:
	$(MAKE) --no-print-directory vet
	$(MAKE) --no-print-directory test
	$(MAKE) --no-print-directory build UI_TARGETS="all wisp-uitest wisp-updatetest"
	$(MAKE) --no-print-directory lint
	scripts/smoke.sh
	python3 scripts/test-update-install.py
	$(UI_BUILD)/wisp-updatetest
	WISP_BIN=$(BIN) $(UI_BUILD)/wisp-uitest

chaos: daemon
	@cmake --log-level=WARNING -S ui -B $(UI_BUILD) $(UI_CMAKE_FLAGS)
	cmake --build $(UI_BUILD) --parallel 2 --target wisp-chaos
	WISP_BIN=$(BIN) $(UI_BUILD)/wisp-chaos

perf: build
	scripts/perf.sh

install: build
	install -Dm755 -t $(BINDIR) $(BIN)/wispd $(BIN)/wispctl $(UI_BUILD)/wisp
	install -d $(APPDIR) $(UNITDIR)
	sed 's|^\(Try\)\?Exec=wisp$$|\1Exec=$(PREFIX)/bin/wisp|' packaging/wisp.desktop > $(APPDIR)/wisp.desktop
	sed 's|%h/.local/bin/wispd|$(PREFIX)/bin/wispd|' packaging/wispd.service > $(UNITDIR)/wispd.service
	ls $(WHATSAPP)/notification.* $(WHATSAPP)/wisp.* $(WHATSAPP)/wallpaper-light.png $(WHATSAPP)/Roboto.woff2 >/dev/null 2>&1 || scripts/fetch-whatsapp-assets.sh $(WHATSAPP) \
		|| echo "WhatsApp's icon and sound are unavailable: installing Wisp's icon and no sound"
	rm -f $(ICONDIR)/scalable/apps/wisp.svg $(ICONDIR)/512x512/apps/wisp.png $(DATADIR)/notification.* $(DATADIR)/wallpaper-*
	icon=$$(ls $(WHATSAPP)/wisp.* 2>/dev/null || echo packaging/wisp.svg); case $$icon in \
		*.svg) install -Dm644 $$icon $(ICONDIR)/scalable/apps/wisp.svg ;; \
		*) install -Dm644 $$icon $(ICONDIR)/512x512/apps/wisp.png ;; esac
	for f in $(WHATSAPP)/notification.* $(WHATSAPP)/wallpaper-*.png; do [ ! -f "$$f" ] || install -Dm644 -t $(DATADIR) "$$f"; done
	for f in $(WHATSAPP)/Roboto*.woff2; do [ ! -f "$$f" ] || install -Dm644 -t $(DATADIR) "$$f"; done
	install -Dm644 ui/assets/fonts/LICENSE $(DATADIR)/Roboto-LICENSE.txt

uninstall:
	rm -f $(BINDIR)/wispd $(BINDIR)/wispctl $(BINDIR)/wisp $(APPDIR)/wisp.desktop \
		$(ICONDIR)/scalable/apps/wisp.svg $(ICONDIR)/512x512/apps/wisp.png $(DATADIR)/notification.* $(DATADIR)/wallpaper-* \
		$(UNITDIR)/wispd.service
	rm -f $(DATADIR)/Roboto*.woff2 $(DATADIR)/Roboto-LICENSE.txt

autostart: install
	systemctl --user daemon-reload && systemctl --user enable wispd.service

no-autostart:
	systemctl --user disable wispd.service

clean:
	rm -rf build bin
