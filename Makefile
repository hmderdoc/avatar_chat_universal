SHELL := /bin/bash

GO ?= go
DOOR_BIN := avatar_chat_universal
SERVER_BIN := avatar_chat_server
DIST := dist

GO_FLAGS := -trimpath -ldflags='-s -w'

# Targets: GOOS_GOARCH
TARGETS := \
	linux_amd64 \
	linux_arm64 \
	linux_386 \
	windows_amd64 \
	windows_386 \
	darwin_amd64 \
	darwin_arm64

.PHONY: all
all: build

.PHONY: build
build:
	CGO_ENABLED=0 $(GO) build $(GO_FLAGS) -o $(DOOR_BIN) ./cmd/$(DOOR_BIN)
	CGO_ENABLED=0 $(GO) build $(GO_FLAGS) -o $(SERVER_BIN) ./cmd/$(SERVER_BIN)

.PHONY: test
test:
	$(GO) test ./...

.PHONY: dist
dist: $(addprefix dist-,$(TARGETS))

dist-%:
	$(eval GOOS := $(word 1,$(subst _, ,$*)))
	$(eval GOARCH := $(word 2,$(subst _, ,$*)))
	$(eval EXT := $(if $(filter windows,$(GOOS)),.exe,))
	$(eval OUT := $(DIST)/$*)
	mkdir -p $(OUT)
	GOOS=$(GOOS) GOARCH=$(GOARCH) CGO_ENABLED=0 \
		$(GO) build $(GO_FLAGS) -o $(OUT)/$(DOOR_BIN)$(EXT) ./cmd/$(DOOR_BIN)
	GOOS=$(GOOS) GOARCH=$(GOARCH) CGO_ENABLED=0 \
		$(GO) build $(GO_FLAGS) -o $(OUT)/$(SERVER_BIN)$(EXT) ./cmd/$(SERVER_BIN)
	cp avatar_chat.ini INSTALL.md $(OUT)/
	cd $(DIST) && tar czf $*.tar.gz $*

.PHONY: clean
clean:
	rm -f $(DOOR_BIN) $(SERVER_BIN) $(DOOR_BIN).exe $(SERVER_BIN).exe
	rm -rf $(DIST)
