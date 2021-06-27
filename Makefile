VERSION = $(shell git describe --abbrev=0 --tags)
FULLVERSION = $(shell git describe --tags)
BUILD = $(shell date +%FT%T%z)
OS = $(shell uname -s | tr '[A-Z]' '[a-z]')
TAR = -X main.WithTarball=ON
BIN = replication-manager
BINDIR = build/binaries
BIN-OSC = $(BIN)-osc
BIN-OSC-CGO = $(BIN)-osc-cgo
BIN-TST = $(BIN)-tst
BIN-PRO = $(BIN)-pro
BIN-ARM = $(BIN)-arm
BIN-CLI = $(BIN)-cli
BIN-ARB = $(BIN)-arb
PROTO_DIR = signal18/replication-manager/v3

all: bin tar cli arb

bin: osc tst pro arm osc-cgo

tar: osc-basedir tst-basedir pro-basedir arm-basedir osc-cgo-basedir

osc:
	env GOOS=$(OS) GOARCH=amd64 go build -v  --ldflags "-extldflags '-static' -w -s -X main.GoOS=$(OS) -X main.GoArch=amd64 -X main.Version=$(VERSION) -X main.FullVersion=$(FULLVERSION) -X main.Build=$(BUILD) -X main.WithProvisioning=OFF "  $(LDFLAGS) -o $(BINDIR)/$(BIN-OSC)

osc-basedir:
	env GOOS=$(OS) GOARCH=amd64  go build -v  --ldflags "-extldflags '-static' -w -s $(TAR) -X main.GoOS=$(OS) -X main.GoArch=amd64 -X main.Version=$(VERSION) -X main.FullVersion=$(FULLVERSION) -X main.Build=$(BUILD) -X main.WithProvisioning=OFF "  $(LDFLAGS) -o $(BINDIR)/$(BIN-OSC)-basedir

osc-cgo:
	env CGO_ENABLED=1 GOOS=$(OS) GOARCH=amd64 go build -v  --ldflags "-extldflags '-static' -w -s -X main.GoOS=$(OS) -X main.GoArch=amd64 -X main.Version=$(VERSION) -X main.FullVersion=$(FULLVERSION) -X main.Build=$(BUILD) -X main.WithProvisioning=OFF "  $(LDFLAGS) -o $(BINDIR)/$(BIN-OSC-CGO)

osc-cgo-basedir:
	env CGO_ENABLED=1 GOOS=$(OS) GOARCH=amd64  go build -v  --ldflags "-extldflags '-static' -w -s $(TAR) -X main.GoOS=$(OS) -X main.GoArch=amd64 -X main.Version=$(VERSION) -X main.FullVersion=$(FULLVERSION) -X main.Build=$(BUILD) -X main.WithProvisioning=OFF "  $(LDFLAGS) -o $(BINDIR)/$(BIN-OSC-CGO)-basedir

tst:
	env GOOS=$(OS) GOARCH=amd64  go build -v  --ldflags "-w -s -X main.GoOS=$(OS) -X main.GoArch=amd64 -X main.Version=$(VERSION) -X main.FullVersion=$(FULLVERSION) -X main.Build=$(BUILD)   -X main.WithDeprecate=OFF"  $(LDFLAGS) -o $(BINDIR)/$(BIN-TST)

tst-basedir:
	env GOOS=$(OS) GOARCH=amd64  go build -v  --ldflags "-w -s $(TAR) -X main.GoOS=$(OS) -X main.GoArch=amd64 -X main.Version=$(VERSION) -X main.FullVersion=$(FULLVERSION) -X main.Build=$(BUILD)   -X main.WithDeprecate=OFF"  $(LDFLAGS) -o $(BINDIR)/$(BIN-TST)-basedir

pro:
	env GOOS=$(OS) GOARCH=amd64  go build -v --ldflags "-w -s -X main.GoOS=$(OS) -X main.GoArch=amd64 -X main.Version=$(VERSION) -X main.FullVersion=$(FULLVERSION) -X main.Build=$(BUILD) -X main.WithOpenSVC=ON  "  $(LDFLAGS) -o $(BINDIR)/$(BIN-PRO)

pro-basedir:
	env GOOS=$(OS) GOARCH=amd64  go build -v  --ldflags "-w -s $(TAR) -X main.GoOS=$(OS) -X main.GoArch=amd64 -X main.Version=$(VERSION) -X main.FullVersion=$(FULLVERSION) -X main.Build=$(BUILD) -X main.WithOpenSVC=ON  "  $(LDFLAGS) -o $(BINDIR)/$(BIN-PRO)-basedir

arm:
	env   GOOS=$(OS) GOARCH=arm64  go build -v  --ldflags "-extldflags '-static' -w -s -X main.GoOS=$(OS) -X main.GoArch=arm64  -X main.FullVersion=$(FULLVERSION) -X main.Build=$(BUILD) -X main.WithOpenSVC=ON  "  $(LDFLAGS) -o $(BINDIR)/$(BIN-ARM)

arm-basedir:
	env  GOOS=$(OS) GOARCH=arm64  go build -v  --ldflags "-extldflags '-static' -w -s -X main.GoOS=$(OS) -X main.GoArch=arm64  -X main.FullVersion=$(FULLVERSION) -X main.Build=$(BUILD) -X main.WithOpenSVC=ON  "  $(LDFLAGS) -o $(BINDIR)/$(BIN-ARM)-basedir

cli:
	env GOOS=$(OS) GOARCH=amd64  go build -v --tags "clients" --ldflags "-w -s -X main.GoOS=$(OS) -X main.GoArch=amd64 -X main.Version=$(VERSION) -X main.FullVersion=$(FULLVERSION) -X main.Build=$(BUILD) -X main.WithOpenSVC=ON  -X main.WithArbitrationClient=OFF "  $(LDFLAGS) -o $(BINDIR)/$(BIN-CLI)

arb:
	env GOOS=$(OS) GOARCH=amd64  go build -v --tags "arbitrator" --ldflags "-w -s -X main.GoOS=$(OS) -X main.GoArch=amd64 -X main.Version=$(VERSION) -X main.FullVersion=$(FULLVERSION) -X main.Build=$(BUILD) -X main.WithOpenSVC=ON  -X main.WithArbitration=ON -X main.WithBackup=OFF"  $(LDFLAGS) -o $(BINDIR)/$(BIN-ARB)

package: all
	nobuild=0 ./package_$(OS)_amd64.sh

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