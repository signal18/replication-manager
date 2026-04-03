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

all: cli bin tar arb

bin: osc tst pro osc-cgo emb plugins

non-cgo: cli osc tst pro arb emb plugins

tar: osc-basedir tst-basedir pro-basedir osc-cgo-basedir

pro osc emb pro-basedir : react plugins

react:
	$(Building react frontend $(REACT))
	@if [ $(WITH_REACT) = "ON" ]; then rm -r ./share/dashboard/assets; npm --prefix=./share/dashboard_react install; npm --prefix=./share/dashboard_react run build; cp -rp ./share/dashboard_react/dist/* ./share/dashboard/; fi

osc:
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

pro:
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
PLUGIN_BINDIR   := build/plugins

# Wire protocol version — read directly from source so it never drifts.
WIRE_VERSION := $(shell grep -m1 'WireVersion = ' cluster/logplugin/plugins/wire/wire.go | awk '{print $$NF}')

# ---- Plugin signing keys & distribution repo --------------------------------
# PLUGIN_SIGNER_REPO is both the key store AND the distribution registry:
#
#   replication-manager-plugin-signer/
#   ├── plugin-signing.key          (private — never leaves CI)
#   ├── plugin-signing.pub          (public  — deployed to repman servers)
#   ├── wire1/                      (binaries built against wire protocol v1)
#   │   ├── plugin-connection-storm
#   │   └── plugin-slow-query-regression ...
#   ├── wire2/                      (future — when wire protocol breaks)
#   └── 3.2.1 -> wire1             (symlink: repman release → wire dir)
#   └── 3.3.0 -> wire2
#
# Your back office reads the client repman version, follows the symlink and
# pulls that wire<n>/ directory into .pull/<cluster>/plugins/.
#
# Set PLUGIN_SIGNER_USER + PLUGIN_SIGNER_TOKEN (GitHub PAT, repo:read+write)
# to fetch keys AND push the built binaries back.
# Without credentials a fresh local keypair is generated — dev builds still
# get signed plugins, just with a local key.

PLUGIN_SIGNER_REPO  ?= https://github.com/signal18/replication-manager-plugin-signer
PLUGIN_SIGNER_USER  ?=
PLUGIN_SIGNER_TOKEN ?=
PLUGIN_KEY_DIR      ?= $(HOME)/.replication-manager
PLUGIN_SIGNING_KEY  ?= $(PLUGIN_KEY_DIR)/plugin-signing.key
PLUGIN_SIGNING_PUB  ?= $(PLUGIN_KEY_DIR)/plugin-signing.pub
PLUGIN_SIG_DIR      := share/plugins

# Temporary clone of the signer repo — populated by plugin-keys, reused by plugin-push.
PLUGIN_SIGNER_CLONE := $(PLUGIN_KEY_DIR)/signer-repo

plugins: $(PLUGIN_NAMES:%=$(PLUGIN_BINDIR)/%) plugin-sigs plugin-push

$(PLUGIN_BINDIR)/%:
	@mkdir -p $(PLUGIN_BINDIR)
	@echo "Building plugin: $*"
	env CGO_ENABLED=0 GOOS=$(OS) GOARCH=$(ARCH) \
	  go build -v \
	    --ldflags "-extldflags '-static' -w -s" \
	    -o $(PLUGIN_BINDIR)/$* \
	    ./cluster/logplugin/plugins/$*/...

# Fetch or generate the plugin signing keypair.
# Leaves the repo clone in PLUGIN_SIGNER_CLONE for plugin-push to reuse.
#
# Priority:
#   1. Keys already present — reuse, skip clone if not needed for push.
#   2. Credentials set — clone repo, copy keys.
#   3. No credentials — generate fresh local keypair.
plugin-keys:
	@mkdir -p $(PLUGIN_KEY_DIR)
	@if [ -n "$(PLUGIN_SIGNER_USER)" ] && [ -n "$(PLUGIN_SIGNER_TOKEN)" ]; then \
		if [ ! -d "$(PLUGIN_SIGNER_CLONE)/.git" ]; then \
			echo "Cloning plugin signer repo..."; \
			AUTH_URL=$$(echo "$(PLUGIN_SIGNER_REPO)" | sed "s|https://|https://$(PLUGIN_SIGNER_USER):$(PLUGIN_SIGNER_TOKEN)@|"); \
			git clone --depth 1 --quiet "$$AUTH_URL" "$(PLUGIN_SIGNER_CLONE)"; \
		else \
			echo "Updating plugin signer repo..."; \
			cd "$(PLUGIN_SIGNER_CLONE)" && git pull --quiet; \
		fi; \
		cp "$(PLUGIN_SIGNER_CLONE)/plugin-signing.key" "$(PLUGIN_SIGNING_KEY)"; \
		cp "$(PLUGIN_SIGNER_CLONE)/plugin-signing.pub" "$(PLUGIN_SIGNING_PUB)"; \
		chmod 600 "$(PLUGIN_SIGNING_KEY)"; \
		echo "Keys fetched from signer repo (wire$(WIRE_VERSION))"; \
	elif [ -f "$(PLUGIN_SIGNING_KEY)" ] && [ -f "$(PLUGIN_SIGNING_PUB)" ]; then \
		echo "Plugin signing keys already present — reusing $(PLUGIN_KEY_DIR)"; \
	else \
		echo "No credentials and no existing keys — generating local keypair"; \
		echo "Set PLUGIN_SIGNER_USER + PLUGIN_SIGNER_TOKEN to use the official Signal18 key."; \
		./$(BINDIR)/$(BIN) plugin-keygen \
			--plugin-private-key "$(PLUGIN_SIGNING_KEY)" \
			--plugin-public-key  "$(PLUGIN_SIGNING_PUB)"; \
	fi

# Sign all built plugin binaries using the key resolved by plugin-keys.
plugin-sigs: plugin-keys
	@mkdir -p $(PLUGIN_SIG_DIR)
	@echo "Signing plugins → $(PLUGIN_SIG_DIR)  [wire$(WIRE_VERSION)]"
	@for name in $(PLUGIN_NAMES); do \
		bin=$(PLUGIN_BINDIR)/$$name; \
		if [ -f $$bin ]; then \
			./$(BINDIR)/$(BIN) plugin-sign \
				--plugin-private-key "$(PLUGIN_SIGNING_KEY)" \
				--sig-output-dir    "$(PLUGIN_SIG_DIR)" \
				$$bin && echo "  signed $$name"; \
		fi; \
	done

# Push built plugins + sigs back to the signer repo under:
#   wire$(WIRE_VERSION)/          — binaries for this wire protocol version
#   $(VERSION) -> wire$(WIRE_VERSION)  — symlink: repman release → wire dir
#
# Only runs when PLUGIN_SIGNER_USER + TOKEN are set (i.e. CI builds).
# Skipped silently for dev/source builds.
plugin-push:
	@if [ -n "$(PLUGIN_SIGNER_USER)" ] && [ -n "$(PLUGIN_SIGNER_TOKEN)" ] && [ -d "$(PLUGIN_SIGNER_CLONE)/.git" ]; then \
		echo "Publishing plugins to signer repo [$(VERSION) → wire$(WIRE_VERSION)]"; \
		WIREDIR="$(PLUGIN_SIGNER_CLONE)/wire$(WIRE_VERSION)"; \
		mkdir -p "$$WIREDIR"; \
		for name in $(PLUGIN_NAMES); do \
			bin=$(PLUGIN_BINDIR)/$$name; \
			if [ -f $$bin ]; then \
				cp $$bin "$$WIREDIR/$$name"; \
				cp "$(PLUGIN_SIG_DIR)/$$name.sig" "$$WIREDIR/$$name.sig" 2>/dev/null || true; \
				echo "  published $$name → wire$(WIRE_VERSION)/"; \
			fi; \
		done; \
		cd "$(PLUGIN_SIGNER_CLONE)" && \
		ln -sfn "wire$(WIRE_VERSION)" "$(VERSION)" && \
		git config user.email "ci@signal18.io" && \
		git config user.name  "replication-manager CI" && \
		git add -A && \
		git diff --cached --quiet || \
		  git commit -m "plugins: $(VERSION) → wire$(WIRE_VERSION) [$(FULLVERSION)]" && \
		AUTH_URL=$$(echo "$(PLUGIN_SIGNER_REPO)" | sed "s|https://|https://$(PLUGIN_SIGNER_USER):$(PLUGIN_SIGNER_TOKEN)@|"); \
		git push "$$AUTH_URL" HEAD:main && \
		echo "Pushed $(VERSION) → wire$(WIRE_VERSION) to signer repo"; \
	else \
		echo "Skipping plugin-push (no credentials or dev build)"; \
	fi

plugins-clean:
	rm -rf $(PLUGIN_BINDIR)
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
