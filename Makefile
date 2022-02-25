VERSION = $(shell git describe --abbrev=0 --tags)
FULLVERSION = $(shell git describe --tags)
BUILD = $(shell date +%FT%T%z)
OS = $(shell uname -s | tr '[A-Z]' '[a-z]')
ARCH ?= amd64
TAR = -X server.WithTarball=ON
BIN = replication-manager
BINDIR = build/binaries
BIN-OSC = $(BIN)-osc
BIN-OSC-CGO = $(BIN)-osc-cgo
BIN-TST = $(BIN)-tst
BIN-PRO = $(BIN)-pro
BIN-CLI = $(BIN)-cli
BIN-ARB = $(BIN)-arb
PROTO_DIR = signal18/replication-manager/v3

all: bin tar cli arb

bin: osc tst pro osc-cgo

non-cgo: osc tst pro arb cli

tar: osc-basedir tst-basedir pro-basedir osc-cgo-basedir

osc:
	env GOOS=$(OS) GOARCH=$(ARCH) go build -v --tags "server" --ldflags "-extldflags '-static' -w -s -X server.Version=$(VERSION) -X server.FullVersion=$(FULLVERSION) -X server.Build=$(BUILD) -X server.WithProvisioning=OFF "  $(LDFLAGS) -o $(BINDIR)/$(BIN-OSC)

osc-basedir:
	env GOOS=$(OS) GOARCH=$(ARCH)  go build -v --tags "server"  --ldflags "-extldflags '-static' -w -s $(TAR) -X server.Version=$(VERSION) -X server.FullVersion=$(FULLVERSION) -X server.Build=$(BUILD) -X server.WithProvisioning=OFF "  $(LDFLAGS) -o $(BINDIR)/$(BIN-OSC)-basedir

osc-cgo:
ifeq ($(ARCH),amd64)
	env CGO_ENABLED=1 GOOS=$(OS) GOARCH=$(ARCH) go build -v --tags "server" --ldflags "-extldflags '-static' -w -s -X server.Version=$(VERSION) -X server.FullVersion=$(FULLVERSION) -X server.Build=$(BUILD) -X server.WithProvisioning=OFF "  $(LDFLAGS) -o $(BINDIR)/$(BIN-OSC-CGO)
endif

osc-cgo-basedir:
ifeq ($(ARCH),amd64)
	env CGO_ENABLED=1 GOOS=$(OS) GOARCH=$(ARCH)  go build -v --tags "server" --ldflags "-extldflags '-static' -w -s $(TAR) -X server.Version=$(VERSION) -X server.FullVersion=$(FULLVERSION) -X server.Build=$(BUILD) -X server.WithProvisioning=OFF "  $(LDFLAGS) -o $(BINDIR)/$(BIN-OSC-CGO)-basedir
endif

tst:
	env GOOS=$(OS) GOARCH=$(ARCH)  go build -v --tags "server" --ldflags "-w -s -X server.Version=$(VERSION) -X server.FullVersion=$(FULLVERSION) -X server.Build=$(BUILD)   -X server.WithDeprecate=OFF"  $(LDFLAGS) -o $(BINDIR)/$(BIN-TST)

tst-basedir:
	env GOOS=$(OS) GOARCH=$(ARCH)  go build -v --tags "server"  --ldflags "-w -s $(TAR) -X server.Version=$(VERSION) -X server.FullVersion=$(FULLVERSION) -X server.Build=$(BUILD)   -X server.WithDeprecate=OFF"  $(LDFLAGS) -o $(BINDIR)/$(BIN-TST)-basedir

pro:
	env GOOS=$(OS) GOARCH=$(ARCH)  go build -v --tags "server" --ldflags "-w -s -X server.Version=$(VERSION) -X server.FullVersion=$(FULLVERSION) -X server.Build=$(BUILD) -X server.WithOpenSVC=ON  "  $(LDFLAGS) -o $(BINDIR)/$(BIN-PRO)

pro-basedir:
	env GOOS=$(OS) GOARCH=$(ARCH)  go build -v --tags "server" --ldflags "-w -s $(TAR) -X server.Version=$(VERSION) -X server.FullVersion=$(FULLVERSION) -X server.Build=$(BUILD) -X server.WithOpenSVC=ON  "  $(LDFLAGS) -o $(BINDIR)/$(BIN-PRO)-basedir

cli:
	env GOOS=$(OS) GOARCH=$(ARCH)  go build -v --tags "clients" --ldflags "-w -s -X clients.Version=$(VERSION) -X clients.FullVersion=$(FULLVERSION) -X clients.Build=$(BUILD)"  $(LDFLAGS) -o $(BINDIR)/$(BIN-CLI)

arb:
	env GOOS=$(OS) GOARCH=$(ARCH)  go build -v --tags "arbitrator" --ldflags "-w -s -X arbitrator.Version=$(VERSION) -X arbitrator.FullVersion=$(FULLVERSION) -X arbitrator.Build=$(BUILD)"   $(LDFLAGS) -o $(BINDIR)/$(BIN-ARB)

package: all
	nobuild=0 ./package_$(OS).sh

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
