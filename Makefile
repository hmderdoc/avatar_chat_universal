SHELL := /bin/bash

GO ?= go
DOOR_BIN := avatar_chat_universal
SERVER_BIN := avatar_chat_server
IRC_BRIDGE_BIN := avatar_chat_irc_bridge
DISCORD_BRIDGE_BIN := avatar_chat_discord_bridge
DIST := dist

GO_FLAGS := -trimpath -ldflags='-s -w'

# Targets: GOOS_GOARCH. Adjust if you support new platforms.
TARGETS := \
	linux_amd64 \
	linux_arm64 \
	linux_386 \
	windows_amd64 \
	windows_386 \
	darwin_amd64 \
	darwin_arm64

# Files we bundle into every distribution tarball (relative to repo root).
# Anything top-level that a sysop would want to read or edit goes here.
DIST_FILES := \
	avatar_chat.ini \
	irc_bridge.ini \
	discord_bridge.ini.example \
	README.md \
	INSTALL.md \
	CONFIG.md \
	THEMING.md \
	BRIDGES.md \
	AVATARS.md \
	SCREENSAVER.md \
	CONTRIBUTING.md \
	CHANGELOG.md \
	LICENSE

.PHONY: all
all: build

# Splash artwork lives at the repo root for discoverability (sysops who
# want a custom splash drop their own ./splash.ans here pre-build); it's
# copied into internal/ui/ where //go:embed can pick it up. Re-run on
# every build so the embedded copy can never go stale.
.PHONY: splash
splash:
	cp splash.ans internal/ui/splash.ans

.PHONY: build
build: splash
	CGO_ENABLED=0 $(GO) build $(GO_FLAGS) -o $(DOOR_BIN) ./cmd/$(DOOR_BIN)
	CGO_ENABLED=0 $(GO) build $(GO_FLAGS) -o $(SERVER_BIN) ./cmd/$(SERVER_BIN)
	CGO_ENABLED=0 $(GO) build $(GO_FLAGS) -o $(IRC_BRIDGE_BIN) ./cmd/$(IRC_BRIDGE_BIN)
	CGO_ENABLED=0 $(GO) build $(GO_FLAGS) -o $(DISCORD_BRIDGE_BIN) ./cmd/$(DISCORD_BRIDGE_BIN)

.PHONY: test
test:
	$(GO) test ./...

.PHONY: dist
dist: $(addprefix dist-,$(TARGETS))

# Build one platform tarball. Usage: make dist-linux_amd64
dist-%: splash
	$(eval GOOS := $(word 1,$(subst _, ,$*)))
	$(eval GOARCH := $(word 2,$(subst _, ,$*)))
	$(eval EXT := $(if $(filter windows,$(GOOS)),.exe,))
	$(eval OUT := $(DIST)/$*)
	rm -rf $(OUT)
	mkdir -p $(OUT)/themes $(OUT)/avatars/sysop $(OUT)/ansi_gallery
	# Compile both binaries fully static.
	GOOS=$(GOOS) GOARCH=$(GOARCH) CGO_ENABLED=0 \
		$(GO) build $(GO_FLAGS) -o $(OUT)/$(DOOR_BIN)$(EXT) ./cmd/$(DOOR_BIN)
	GOOS=$(GOOS) GOARCH=$(GOARCH) CGO_ENABLED=0 \
		$(GO) build $(GO_FLAGS) -o $(OUT)/$(SERVER_BIN)$(EXT) ./cmd/$(SERVER_BIN)
	GOOS=$(GOOS) GOARCH=$(GOARCH) CGO_ENABLED=0 \
		$(GO) build $(GO_FLAGS) -o $(OUT)/$(IRC_BRIDGE_BIN)$(EXT) ./cmd/$(IRC_BRIDGE_BIN)
	GOOS=$(GOOS) GOARCH=$(GOARCH) CGO_ENABLED=0 \
		$(GO) build $(GO_FLAGS) -o $(OUT)/$(DISCORD_BRIDGE_BIN)$(EXT) ./cmd/$(DISCORD_BRIDGE_BIN)
	# Top-level files (config, full doc set, license). Splash artwork is
	# embedded in the binary; nothing to ship separately.
	cp $(DIST_FILES) $(OUT)/
	cp discord_bridge.ini.example $(OUT)/discord_bridge.ini
	# Bundled themes -- futurewave is the default; sysops copy + edit.
	cp themes/*.ini $(OUT)/themes/
	# Stub READMEs for the directories sysops fill with their own content.
	# Generated inline so GHA + a fresh `make dist` produce identical output.
	@printf '%s\n' \
		'Drop sysop-curated .bin avatar collections here.' \
		'' \
		'Each .bin file becomes a separate "collection" in the in-door' \
		'avatar selector, named after the filename. The format is' \
		'concatenated 120-byte avatars (see AVATARS.md). SAUCE records' \
		'are tolerated and stripped automatically.' \
		'' \
		'To enable scanning of this directory, uncomment the' \
		'`sysop_avatars_dir` line in avatar_chat.ini. Top-level files' \
		'only -- subdirectories are not scanned.' \
		> $(OUT)/avatars/sysop/README.txt
	@printf '%s\n' \
		'Drop SAUCE-tagged ANSI / BIN artwork here for the in-door' \
		'screensaver.' \
		'' \
		'The "ansi_gallery" idle animation scrolls each piece' \
		'vertically through the chat transcript area while users idle.' \
		'Subdirectories are walked recursively, organize however you' \
		'like.' \
		'' \
		'Wider art (132 / 160-col) is clipped at the right edge without' \
		'distorting alignment; narrower is centered horizontally.' \
		'' \
		'Suggested source: clone the sixteencolors archive' \
		'(https://github.com/blocktronics/sixteencolors-archive) and' \
		'point ansi_gallery_dir at it.' \
		'' \
		'To turn the gallery on, uncomment ansi_gallery_dir in' \
		'avatar_chat.ini. See SCREENSAVER.md for tuning the rotation' \
		'and the "interleave" behavior.' \
		> $(OUT)/ansi_gallery/README.txt
	# Tar it up.
	cd $(DIST) && tar czf avatar_chat_universal_$*.tar.gz $*

.PHONY: clean
clean:
	rm -f $(DOOR_BIN) $(SERVER_BIN) $(DOOR_BIN).exe $(SERVER_BIN).exe
	rm -f $(IRC_BRIDGE_BIN) $(IRC_BRIDGE_BIN).exe
	rm -f $(DISCORD_BRIDGE_BIN) $(DISCORD_BRIDGE_BIN).exe
	rm -rf $(DIST)

# --- Legacy Windows (XP) target -------------------------------------
# Cross-compile a windows/386 binary that runs on Windows XP. Requires
# Go 1.10.x at ~/.local/go1.10/bin/go (last toolchain that emits XP-
# loadable PE binaries). The build script runs in a temporary GOPATH
# so your real Go install isn't touched. See compat/_legacy/README.md
# for prerequisites and details.
#
# Output: dist/windows_386_xp/avatar_chat_universal.exe
#         dist/avatar_chat_universal_windows_386_xp.tar.gz
.PHONY: dist-windows-xp
dist-windows-xp: splash
	bash compat/_legacy/build-windows-xp.sh
