VERSION = $(shell git describe --abbrev=0 --tags)
FULLVERSION = $(shell git describe --tags)
BUILD = $(shell date +%FT%T%z)
OS = $(shell uname -s | tr '[A-Z]' '[a-z]')
ARCH ?= amd64
TAR = -X github.com/signal18/replication-manager/server.WithTarball=ON
BIN = replication-manager
BINDIR = build/binaries
BIN-OSC = $(BIN)-osc
BIN-OSC-CGO = $(BIN)-osc-cgo
BIN-TST = $(BIN)-tst
BIN-PRO = $(BIN)-pro
BIN-CLI = $(BIN)-cli
BIN-ARB = $(BIN)-arb
BIN-EMBED = $(BIN)

PROTO_DIR = signal18/replication-manager/v3
EMBED = -X github.com/signal18/replication-manager/server.WithEmbed=ON
WITH_REACT = ON

.PHONY: all bin non-cgo tar react osc osc-bin osc-basedir osc-cgo osc-cgo-basedir \
        tst tst-basedir pro pro-bin pro-basedir cli arb emb \
        plugins plugin-keys plugin-sigs plugin-push plugin-repo-init plugins-clean \
        clean proto

all: cli bin tar arb

bin: osc tst pro osc-cgo emb

non-cgo: cli osc tst pro arb emb plugins

tar: osc-basedir tst-basedir pro-basedir osc-cgo-basedir

pro osc emb pro-basedir : react

react:
	$(Building react frontend $(REACT))
	@if [ $(WITH_REACT) = "ON" ]; then rm -rf ./share/dashboard/assets; npm --prefix=./share/dashboard_react install; npm --prefix=./share/dashboard_react run build; cp -rp ./share/dashboard_react/dist/* ./share/dashboard/; fi

osc: osc-bin plugins

osc-bin:
	env CGO_ENABLED=0 GOOS=$(OS) GOARCH=$(ARCH) go build -v --tags "server" --ldflags "-extldflags '-static' -w -s -X github.com/signal18/replication-manager/server.Version=$(VERSION) -X github.com/signal18/replication-manager/server.FullVersion=$(FULLVERSION) -X github.com/signal18/replication-manager/server.Build=$(BUILD) -X github.com/signal18/replication-manager/server.WithProvisioning=OFF "  $(LDFLAGS) -o $(BINDIR)/$(BIN-OSC)

osc-basedir:
	env CGO_ENABLED=0 GOOS=$(OS) GOARCH=$(ARCH)  go build -v --tags "server"  --ldflags "-extldflags '-static' -w -s $(TAR) -X github.com/signal18/replication-manager/server.Version=$(VERSION) -X github.com/signal18/replication-manager/server.FullVersion=$(FULLVERSION) -X github.com/signal18/replication-manager/server.Build=$(BUILD) -X github.com/signal18/replication-manager/server.WithProvisioning=OFF "  $(LDFLAGS) -o $(BINDIR)/$(BIN-OSC)-basedir

osc-cgo:
ifeq ($(ARCH),amd64)
	env CGO_ENABLED=1 GOOS=$(OS) GOARCH=$(ARCH) go build -v --tags "server" --ldflags "-extldflags '-static' -w -s -X github.com/signal18/replication-manager/server.Version=$(VERSION) -X github.com/signal18/replication-manager/server.FullVersion=$(FULLVERSION) -X github.com/signal18/replication-manager/server.Build=$(BUILD) -X github.com/signal18/replication-manager/server.WithProvisioning=OFF "  $(LDFLAGS) -o $(BINDIR)/$(BIN-OSC-CGO)
endif

osc-cgo-basedir:
ifeq ($(ARCH),amd64)
	env CGO_ENABLED=1 GOOS=$(OS) GOARCH=$(ARCH)  go build -v --tags "server" --ldflags "-extldflags '-static' -w -s $(TAR) -X github.com/signal18/replication-manager/server.Version=$(VERSION) -X github.com/signal18/replication-manager/server.FullVersion=$(FULLVERSION) -X github.com/signal18/replication-manager/server.Build=$(BUILD) -X github.com/signal18/replication-manager/server.WithProvisioning=OFF "  $(LDFLAGS) -o $(BINDIR)/$(BIN-OSC-CGO)-basedir
endif

tst:
	env CGO_ENABLED=0 GOOS=$(OS) GOARCH=$(ARCH)  go build -v --tags "server" --ldflags "-w -s -X github.com/signal18/replication-manager/server.Version=$(VERSION) -X github.com/signal18/replication-manager/server.FullVersion=$(FULLVERSION) -X github.com/signal18/replication-manager/server.Build=$(BUILD)   -X github.com/signal18/replication-manager/server.WithDeprecate=OFF"  $(LDFLAGS) -o $(BINDIR)/$(BIN-TST)

tst-basedir:
	env CGO_ENABLED=0 GOOS=$(OS) GOARCH=$(ARCH)  go build -v --tags "server"  --ldflags "-w -s $(TAR) -X github.com/signal18/replication-manager/server.Version=$(VERSION) -X github.com/signal18/replication-manager/server.FullVersion=$(FULLVERSION) -X github.com/signal18/replication-manager/server.Build=$(BUILD)   -X github.com/signal18/replication-manager/server.WithDeprecate=OFF"  $(LDFLAGS) -o $(BINDIR)/$(BIN-TST)-basedir

pro: pro-bin plugins

pro-bin:
	env CGO_ENABLED=0 GOOS=$(OS) GOARCH=$(ARCH)  go build -v --tags "server" --ldflags " -w -s -X 'github.com/signal18/replication-manager/server.Version=$(VERSION)' -X 'github.com/signal18/replication-manager/server.FullVersion=$(FULLVERSION)' -X 'github.com/signal18/replication-manager/server.Build=$(BUILD)' -X github.com/signal18/replication-manager/server.WithOpenSVC=ON  "  $(LDFLAGS) -o $(BINDIR)/$(BIN-PRO)

pro-basedir:
	env CGO_ENABLED=0 GOOS=$(OS) GOARCH=$(ARCH)  go build -v --tags "server" --ldflags "-w -s $(TAR) -X github.com/signal18/replication-manager/server.Version=$(VERSION) -X github.com/signal18/replication-manager/server.FullVersion=$(FULLVERSION) -X github.com/signal18/replication-manager/server.Build=$(BUILD) -X github.com/signal18/replication-manager/server.WithOpenSVC=ON  "  $(LDFLAGS) -o $(BINDIR)/$(BIN-PRO)-basedir

cli:
	env CGO_ENABLED=0 GOOS=$(OS) GOARCH=$(ARCH)  go build -v --tags "clients" --ldflags "-w -s $(EMBED) -X github.com/signal18/replication-manager/clients.Version=$(VERSION) -X github.com/signal18/replication-manager/clients.FullVersion=$(FULLVERSION) -X github.com/signal18/replication-manager/clients.Build=$(BUILD)"  $(LDFLAGS) -o $(BINDIR)/$(BIN-CLI)
	mkdir -p ./share/dashboard/static/configurator/bin
	cp $(BINDIR)/$(BIN-CLI) ./share/dashboard/static/configurator/bin/$(BIN-CLI)

arb:
	env CGO_ENABLED=0 GOOS=$(OS) GOARCH=$(ARCH)  go build -v --tags "arbitrator" --ldflags "-w -s -X github.com/signal18/replication-manager/arbitrator.Version=$(VERSION) -X github.com/signal18/replication-manager/arbitrator.FullVersion=$(FULLVERSION) -X github.com/signal18/replication-manager/arbitrator.Build=$(BUILD)"   $(LDFLAGS) -o $(BINDIR)/$(BIN-ARB)

emb:
	env CGO_ENABLED=0 GOOS=$(OS) GOARCH=$(ARCH)  go build -v --tags "server" --ldflags "-w -s $(EMBED) -X 'github.com/signal18/replication-manager/server.Version=$(VERSION)' -X 'github.com/signal18/replication-manager/server.FullVersion=$(FULLVERSION)' -X 'github.com/signal18/replication-manager/server.Build=$(BUILD)' -X github.com/signal18/replication-manager/server.WithOpenSVC=ON  "  $(LDFLAGS) -o $(BINDIR)/$(BIN)

# ---- External log-tailer plugins --------------------------------------------
# Each subdirectory under cluster/logplugin/plugins/ that contains a main.go
# is built as a standalone plugin binary under build/plugins/.
#
# Usage:  make plugins
#         make plugins GOOS=linux GOARCH=amd64

PLUGIN_SRC_DIRS := $(shell find cluster/logplugin/plugins -mindepth 2 -maxdepth 2 -name "main.go" -exec dirname {} \; 2>/dev/null)
PLUGIN_NAMES    := $(notdir $(PLUGIN_SRC_DIRS))
# Common sources shared by all plugins — any change here triggers a full rebuild.
PLUGIN_COMMON_SRCS := $(wildcard cluster/logplugin/*.go) $(wildcard cluster/logplugin/plugins/wire/*.go)
PLUGIN_BINDIR   := build/plugins

# Wire protocol version — read directly from source so it never drifts.
WIRE_VERSION := $(shell grep -m1 'WireVersion = ' cluster/logplugin/plugins/wire/wire.go | awk '{print $$NF}')

# Standalone plugin signing tool — no repman-server dependency.
PLUGIN_SIGNER_BIN := build/tools/plugin-signer

# ---- Plugin signing keys & distribution repo --------------------------------
# PLUGIN_SIGNER_REPO layout (per OS/arch, per wire protocol version):
#
#   replication-manager-plugin-signer/
#   ├── plugin-signing.key                        (private — never leaves CI)
#   ├── plugin-signing.pub                        (public  — deployed to repman servers)
#   ├── plugins/
#   │   ├── linux-amd64/
#   │   │   └── wire-v1/
#   │   │       ├── plugin-connection-storm
#   │   │       ├── plugin-connection-storm.sig
#   │   │       └── plugin-slow-query-regression ...
#   │   ├── linux-arm64/
#   │   │   └── wire-v1/ ...
#   │   └── darwin-arm64/
#   │       └── wire-v1/ ...
#   └── linux-amd64/
#       └── replication-manager-v3.2.1 -> ../plugins/linux-amd64/wire-v1/
#
# The back office reads the repman version and OS/arch of the server, resolves
# the symlink, and downloads the entire wire-v<N>/ directory into
# .pull/<cluster>/plugins/.
#
# Set PLUGIN_SIGNER_USER + PLUGIN_SIGNER_TOKEN (GitHub PAT, repo:read+write)
# to fetch keys AND push built binaries.
# Without credentials a fresh local keypair is generated — dev builds still
# get signed plugins, just with a local key.

PLUGIN_SIGNER_REPO  ?= https://github.com/signal18/replication-manager-plugin-signer
PLUGIN_SIGNER_USER  ?=
PLUGIN_SIGNER_TOKEN ?=
PLUGIN_PUSH         ?= OFF
PLUGIN_KEY_DIR      ?= $(HOME)/.replication-manager
PLUGIN_SIGNING_KEY  ?= $(PLUGIN_KEY_DIR)/plugin-signing.key
PLUGIN_SIGNING_PUB  ?= $(PLUGIN_KEY_DIR)/plugin-signing.pub
PLUGIN_SIG_DIR      := share/plugins

# Combined OS-arch string used as the per-platform subdirectory in the signer repo.
PLUGIN_PLATFORM     := $(OS)-$(ARCH)

# Temporary clone of the signer repo — populated by plugin-keys, reused by plugin-push.
PLUGIN_SIGNER_CLONE := $(PLUGIN_KEY_DIR)/signer-repo

# plugins is always rebuilt unconditionally (.PHONY).
# Incremental builds are handled by Go's own build cache, which is more
# reliable than Make timestamp tracking (Docker COPY flattens all mtimes).
.PHONY: plugins
plugins: $(PLUGIN_SIGNER_BIN)
	@mkdir -p $(PLUGIN_BINDIR)
	@for name in $(PLUGIN_NAMES); do \
		echo "Building plugin: $$name"; \
		env CGO_ENABLED=0 GOOS=$(OS) GOARCH=$(ARCH) \
		  go build -v \
		    --ldflags "-extldflags '-static' -w -s" \
		    -o $(PLUGIN_BINDIR)/$$name \
		    ./cluster/logplugin/plugins/$$name/... || exit 1; \
	done
	$(MAKE) plugin-sigs
	@if [ "$(PLUGIN_PUSH)" = "ON" ]; then \
		$(MAKE) plugin-push; \
	elif [ -n "$(PLUGIN_SIGNER_USER)" ] && [ -n "$(PLUGIN_SIGNER_TOKEN)" ] && [ -d "$(PLUGIN_SIGNER_CLONE)/.git" ]; then \
		WIREDIR="$(PLUGIN_SIGNER_CLONE)/plugins/$(PLUGIN_PLATFORM)/wire-v$(WIRE_VERSION)"; \
		if [ ! -d "$$WIREDIR" ]; then \
			echo "New wire version detected (wire-v$(WIRE_VERSION)) — auto-pushing to signer repo"; \
			$(MAKE) plugin-push; \
		fi; \
	fi

$(PLUGIN_SIGNER_BIN):
	@mkdir -p $(dir $(PLUGIN_SIGNER_BIN))
	env CGO_ENABLED=0 GOOS=$(OS) GOARCH=$(ARCH) \
	  go build -v -o $(PLUGIN_SIGNER_BIN) ./tools/plugin-signer/...

# Bootstrap an empty signer repo: generate keypair, commit both keys, push.
# Run once before the first 'make plugins' with credentials.
plugin-repo-init: $(PLUGIN_SIGNER_BIN)
	@if [ -z "$(PLUGIN_SIGNER_USER)" ] || [ -z "$(PLUGIN_SIGNER_TOKEN)" ]; then \
		echo "ERROR: set PLUGIN_SIGNER_USER and PLUGIN_SIGNER_TOKEN"; exit 1; \
	fi
	@mkdir -p $(PLUGIN_KEY_DIR)
	@AUTH_URL=$$(echo "$(PLUGIN_SIGNER_REPO)" | sed "s|https://|https://$(PLUGIN_SIGNER_USER):$(PLUGIN_SIGNER_TOKEN)@|"); \
	if [ ! -d "$(PLUGIN_SIGNER_CLONE)/.git" ]; then \
		echo "Cloning signer repo..."; \
		git clone --quiet "$$AUTH_URL" "$(PLUGIN_SIGNER_CLONE)" 2>/dev/null || \
		  { mkdir -p "$(PLUGIN_SIGNER_CLONE)" && cd "$(PLUGIN_SIGNER_CLONE)" \
		    && git init && git remote add origin "$$AUTH_URL"; }; \
	fi; \
	if [ ! -f "$(PLUGIN_SIGNING_KEY)" ]; then \
		echo "Generating keypair..."; \
		$(PLUGIN_SIGNER_BIN) keygen \
			--private-key "$(PLUGIN_SIGNING_KEY)" \
			--public-key  "$(PLUGIN_SIGNING_PUB)"; \
	else \
		echo "Keys already present — reusing $(PLUGIN_KEY_DIR)"; \
	fi; \
	cp "$(PLUGIN_SIGNING_KEY)" "$(PLUGIN_SIGNER_CLONE)/plugin-signing.key"; \
	cp "$(PLUGIN_SIGNING_PUB)" "$(PLUGIN_SIGNER_CLONE)/plugin-signing.pub"; \
	cd "$(PLUGIN_SIGNER_CLONE)" && \
	git config user.email "ci@signal18.io" && \
	git config user.name  "replication-manager CI" && \
	git add plugin-signing.key plugin-signing.pub && \
	git diff --cached --quiet || \
	  git commit -m "init: add plugin signing keypair" && \
	git -c http.postBuffer=104857600 push "$$AUTH_URL" HEAD:main && \
	echo "Signer repo initialised with keypair."

# Fetch or generate the plugin signing keypair.
# Leaves the repo clone in PLUGIN_SIGNER_CLONE for plugin-push to reuse.
#
# Priority:
#   1. Credentials set — clone/pull signer repo, copy keys.
#      Fails with a clear message if the repo is empty (run plugin-repo-init first).
#   2. Keys already present locally — reuse.
#   3. No credentials — generate a fresh local keypair (dev build).
plugin-keys: $(PLUGIN_SIGNER_BIN)
	@mkdir -p $(PLUGIN_KEY_DIR)
	@if [ -n "$(PLUGIN_SIGNER_USER)" ] && [ -n "$(PLUGIN_SIGNER_TOKEN)" ]; then \
		AUTH_URL=$$(echo "$(PLUGIN_SIGNER_REPO)" | sed "s|https://|https://$(PLUGIN_SIGNER_USER):$(PLUGIN_SIGNER_TOKEN)@|"); \
		if [ ! -d "$(PLUGIN_SIGNER_CLONE)/.git" ]; then \
			echo "Cloning plugin signer repo..."; \
			git clone --depth 1 --quiet "$$AUTH_URL" "$(PLUGIN_SIGNER_CLONE)"; \
		else \
			echo "Updating plugin signer repo..."; \
			cd "$(PLUGIN_SIGNER_CLONE)" && \
			  git fetch --quiet "$$AUTH_URL" main && \
			  git merge --ff-only FETCH_HEAD --quiet 2>/dev/null || true; \
		fi; \
		if [ ! -f "$(PLUGIN_SIGNER_CLONE)/plugin-signing.key" ]; then \
			echo "ERROR: signer repo contains no keys yet."; \
			echo "  Run: make plugin-repo-init PLUGIN_SIGNER_USER=$(PLUGIN_SIGNER_USER) PLUGIN_SIGNER_TOKEN=<token>"; \
			exit 1; \
		fi; \
		cp "$(PLUGIN_SIGNER_CLONE)/plugin-signing.key" "$(PLUGIN_SIGNING_KEY)"; \
		cp "$(PLUGIN_SIGNER_CLONE)/plugin-signing.pub" "$(PLUGIN_SIGNING_PUB)"; \
		chmod 600 "$(PLUGIN_SIGNING_KEY)"; \
		echo "Keys fetched from signer repo [wire-v$(WIRE_VERSION)]"; \
	elif [ -f "$(PLUGIN_SIGNING_KEY)" ] && [ -f "$(PLUGIN_SIGNING_PUB)" ]; then \
		echo "Plugin signing keys already present — reusing $(PLUGIN_KEY_DIR)"; \
	else \
		echo "No credentials — generating local keypair with $(PLUGIN_SIGNER_BIN)"; \
		echo "Set PLUGIN_SIGNER_USER + PLUGIN_SIGNER_TOKEN to use the official Signal18 key."; \
		$(PLUGIN_SIGNER_BIN) keygen \
			--private-key "$(PLUGIN_SIGNING_KEY)" \
			--public-key  "$(PLUGIN_SIGNING_PUB)"; \
	fi

# Sign all built plugin binaries using the key resolved by plugin-keys.
# The public key is copied into share/plugins/ so it is included in every
# package/Docker image and can be used for runtime signature verification.
plugin-sigs: plugin-keys
	@mkdir -p $(PLUGIN_SIG_DIR)
	@echo "Signing plugins → $(PLUGIN_SIG_DIR)  [wire-v$(WIRE_VERSION)]"
	@for name in $(PLUGIN_NAMES); do \
		bin=$(PLUGIN_BINDIR)/$$name; \
		if [ -f $$bin ]; then \
			$(PLUGIN_SIGNER_BIN) sign \
				--private-key "$(PLUGIN_SIGNING_KEY)" \
				--sig-dir     "$(PLUGIN_SIG_DIR)" \
				$$bin && echo "  signed $$name"; \
		fi; \
	done
	@cp "$(PLUGIN_SIGNING_PUB)" "$(PLUGIN_SIG_DIR)/plugin-signing.pub"
	@echo "Public key → $(PLUGIN_SIG_DIR)/plugin-signing.pub"

# Push built plugins + sigs back to the signer repo under:
#   plugins/$(PLUGIN_PLATFORM)/wire-v$(WIRE_VERSION)/   — binaries + .sig files
#   $(PLUGIN_PLATFORM)/replication-manager-$(VERSION)   — symlink → ../plugins/…/wire-v$(WIRE_VERSION)/
#
# Only runs when PLUGIN_SIGNER_USER + TOKEN are set (i.e. CI builds).
# Skipped silently for dev/source builds.
plugin-push:
	@if [ -n "$(PLUGIN_SIGNER_USER)" ] && [ -n "$(PLUGIN_SIGNER_TOKEN)" ] && [ -d "$(PLUGIN_SIGNER_CLONE)/.git" ]; then \
		echo "Publishing plugins to signer repo [$(VERSION) → $(PLUGIN_PLATFORM)/wire-v$(WIRE_VERSION)]"; \
		WIREDIR="$(PLUGIN_SIGNER_CLONE)/plugins/$(PLUGIN_PLATFORM)/wire-v$(WIRE_VERSION)"; \
		mkdir -p "$$WIREDIR"; \
		for name in $(PLUGIN_NAMES); do \
			bin=$(PLUGIN_BINDIR)/$$name; \
			if [ -f $$bin ]; then \
				cp $$bin "$$WIREDIR/$$name"; \
				echo "  published $$name → plugins/$(PLUGIN_PLATFORM)/wire-v$(WIRE_VERSION)/"; \
			fi; \
		done; \
		SYMLINK_DIR="$(PLUGIN_SIGNER_CLONE)/$(PLUGIN_PLATFORM)"; \
		mkdir -p "$$SYMLINK_DIR"; \
		cd "$(PLUGIN_SIGNER_CLONE)" && \
		ln -sfn "../plugins/$(PLUGIN_PLATFORM)/wire-v$(WIRE_VERSION)" \
		        "$(PLUGIN_PLATFORM)/replication-manager-$(VERSION)" && \
		git config user.email "ci@signal18.io" && \
		git config user.name  "replication-manager CI" && \
		git add -A && \
		git diff --cached --quiet || \
		  git commit -m "plugins: $(VERSION) [$(PLUGIN_PLATFORM)] → wire-v$(WIRE_VERSION) [$(FULLVERSION)]" && \
		AUTH_URL=$$(echo "$(PLUGIN_SIGNER_REPO)" | sed "s|https://|https://$(PLUGIN_SIGNER_USER):$(PLUGIN_SIGNER_TOKEN)@|"); \
		git -c http.postBuffer=104857600 push "$$AUTH_URL" HEAD:main && \
		echo "Pushed $(VERSION) → $(PLUGIN_PLATFORM)/wire-v$(WIRE_VERSION) to signer repo"; \
	else \
		echo "Skipping plugin-push (no credentials or dev build)"; \
	fi

plugins-clean:
	rm -rf $(PLUGIN_BINDIR)
	rm -f $(PLUGIN_SIGNER_BIN)
	rm -f $(PLUGIN_SIG_DIR)/plugin-*.sig
	rm -rf $(PLUGIN_SIGNER_CLONE)


clean:
	find $(BINDIR) -type f | xargs rm

proto:
	@protoc/bin/protoc \
		-I ${PROTO_DIR} \
		-I googleapis/ \
		--go_opt=paths=source_relative \
		--go_out=repmanv3 \
		--go-grpc_opt=paths=source_relative \
		--go-grpc_out=repmanv3 \
		--grpc-gateway_opt logtostderr=true \
		--grpc-gateway_opt paths=source_relative \
		--grpc-gateway_out repmanv3 \
		--openapiv2_out repmanv3 \
		--openapiv2_opt logtostderr=true \
		--openapiv2_opt allow_merge=true \
		--openapiv2_opt merge_file_name=repmanv3 \
		-orepmanv3/service.desc \
		${PROTO_DIR}/cluster.proto ${PROTO_DIR}/messages.proto
