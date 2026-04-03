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
# Subscription plugins delivered via GitLab follow the same pattern.
#
# Usage:  make plugins
#         make plugins GOOS=linux GOARCH=amd64
#
# Find only dirs that contain a main.go (i.e. actual plugin binaries, not library packages)
PLUGIN_SRC_DIRS := $(shell find cluster/logplugin/plugins -mindepth 2 -maxdepth 2 -name "main.go" -exec dirname {} \; 2>/dev/null)
PLUGIN_NAMES    := $(notdir $(PLUGIN_SRC_DIRS))
PLUGIN_BINDIR   := build/plugins

# ---- Plugin signing keys ----------------------------------------------------
# The Ed25519 signing keypair is fetched from a private GitHub repo at build
# time.  Set PLUGIN_SIGNER_USER + PLUGIN_SIGNER_TOKEN (GitHub PAT with
# repo:read scope) to pull the official Signal18 keys.
#
# If credentials are absent the target generates a fresh local keypair so
# developers and users building from source still get signed plugins — they
# just sign with their own local key.
#
# Keys are stored in PLUGIN_KEY_DIR (~/.replication-manager by default) and
# reused across builds without being committed to source.
#
# Override paths if you manage keys differently:
#   make plugins PLUGIN_SIGNING_KEY=/path/to/plugin-signing.key
#
# .sig files land in share/plugins/ and ship with the repman release package.
# They are NOT placed in .pull repos — that separation is the security guarantee.

PLUGIN_SIGNER_REPO ?= https://github.com/signal18/replication-manager-plugin-signer
PLUGIN_SIGNER_USER  ?=
PLUGIN_SIGNER_TOKEN ?=
PLUGIN_KEY_DIR      ?= $(HOME)/.replication-manager
PLUGIN_SIGNING_KEY  ?= $(PLUGIN_KEY_DIR)/plugin-signing.key
PLUGIN_SIGNING_PUB  ?= $(PLUGIN_KEY_DIR)/plugin-signing.pub
PLUGIN_SIG_DIR      := share/plugins

plugins: $(PLUGIN_NAMES:%=$(PLUGIN_BINDIR)/%) plugin-sigs

$(PLUGIN_BINDIR)/%:
	@mkdir -p $(PLUGIN_BINDIR)
	@echo "Building plugin: $*"
	env CGO_ENABLED=0 GOOS=$(OS) GOARCH=$(ARCH) \
	  go build -v \
	    --ldflags "-extldflags '-static' -w -s" \
	    -o $(PLUGIN_BINDIR)/$* \
	    ./cluster/logplugin/plugins/$*/...

# Fetch or generate the plugin signing keypair.
#
# Priority:
#   1. Keys already present at PLUGIN_SIGNING_KEY/PUB paths — reuse, no fetch.
#   2. PLUGIN_SIGNER_USER + TOKEN set — clone private repo, copy keys.
#   3. Neither — generate a fresh local keypair (dev / source builds).
plugin-keys:
	@mkdir -p $(PLUGIN_KEY_DIR)
	@if [ -f "$(PLUGIN_SIGNING_KEY)" ] && [ -f "$(PLUGIN_SIGNING_PUB)" ]; then \
		echo "Plugin signing keys already present — reusing $(PLUGIN_KEY_DIR)"; \
	elif [ -n "$(PLUGIN_SIGNER_USER)" ] && [ -n "$(PLUGIN_SIGNER_TOKEN)" ]; then \
		echo "Fetching plugin signing keys from $(PLUGIN_SIGNER_REPO)"; \
		TMPDIR=$$(mktemp -d); \
		AUTH_URL=$$(echo "$(PLUGIN_SIGNER_REPO)" | sed "s|https://|https://$(PLUGIN_SIGNER_USER):$(PLUGIN_SIGNER_TOKEN)@|"); \
		git clone --depth 1 --quiet "$$AUTH_URL" "$$TMPDIR/signer" && \
		cp "$$TMPDIR/signer/plugin-signing.key" "$(PLUGIN_SIGNING_KEY)" && \
		cp "$$TMPDIR/signer/plugin-signing.pub" "$(PLUGIN_SIGNING_PUB)" && \
		chmod 600 "$(PLUGIN_SIGNING_KEY)" && \
		rm -rf "$$TMPDIR" && \
		echo "Keys fetched successfully"; \
	else \
		echo "No credentials set and no existing keys — generating local keypair"; \
		echo "Set PLUGIN_SIGNER_USER + PLUGIN_SIGNER_TOKEN to use the official Signal18 key."; \
		./$(BINDIR)/$(BIN) plugin-keygen \
			--plugin-private-key "$(PLUGIN_SIGNING_KEY)" \
			--plugin-public-key  "$(PLUGIN_SIGNING_PUB)"; \
	fi

# Sign all built plugin binaries using the key resolved by plugin-keys.
# .sig files go to share/plugins/ to ship with the repman release.
plugin-sigs: plugin-keys
	@mkdir -p $(PLUGIN_SIG_DIR)
	@echo "Signing plugins with key $(PLUGIN_SIGNING_KEY)"
	@for name in $(PLUGIN_NAMES); do \
		bin=$(PLUGIN_BINDIR)/$$name; \
		if [ -f $$bin ]; then \
			./$(BINDIR)/$(BIN) plugin-sign \
				--plugin-private-key "$(PLUGIN_SIGNING_KEY)" \
				--sig-output-dir    "$(PLUGIN_SIG_DIR)" \
				$$bin && echo "  signed $$name"; \
		fi; \
	done

plugins-clean:
	rm -rf $(PLUGIN_BINDIR)
	rm -f $(PLUGIN_SIG_DIR)/plugin-*.sig


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
