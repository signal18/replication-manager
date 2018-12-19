VERSION = $(shell git describe --abbrev=0 --tags)
FULLVERSION = $(shell git describe --tags)
BUILD = $(shell date +%FT%T%z)
OS = $(shell uname -s | tr '[A-Z]' '[a-z]')
TAR = -X main.WithTarball=ON
BIN = replication-manager
BINDIR = build/binaries
BIN-OSC = $(BIN)-osc
BIN-OSC-CGO = $(BIN)-osc-tgo
BIN-TST = $(BIN)-tst
BIN-PRO = $(BIN)-pro
BIN-MIN = $(BIN)-min
BIN-CLI = $(BIN)-cli
BIN-ARB = $(BIN)-arb

all: bin tar cli arb

bin: osc osc-cgo tst pro min

tar: osc-basedir tst-basedir pro-basedir min-basedir

osc:
	env CGO_ENABLED=0 GOOS=$(OS) GOARCH=amd64 go build -v --tags "netgo server" --ldflags "-extldflags 'static' -w -s -X main.GoOS=$(OS) -X main.GoArch=amd64 -X main.Version=$(VERSION) -X main.FullVersion=$(FULLVERSION) -X main.Build=$(BUILD) -X main.WithProvisioning=OFF -X main.WithOpenSVC=OFF -X main.WithHaproxy=ON -X main.WithMaxscale=ON  -X main.WithMariadbshardproxy=ON -X  main.WithProxysql=ON -X  main.WithSphinx=ON -X main.WithArbitration=OFF -X main.WithArbitrationClient=ON -X main.WithMonitoring=ON -X main.WithHttp=ON -X main.WithBackup=ON -X main.WithMail=ON -X main.WithEnforce=ON -X main.WithDeprecate=ON"  $(LDFLAGS) -o $(BINDIR)/$(BIN-OSC)

osc-cgo:
		env CGO_ENABLED=0 GOOS=$(OS) GOARCH=amd64 go build -v --tags "netcgo server" --ldflags  "-w -s -X main.GoOS=$(OS) -X main.GoArch=amd64 -X main.Version=$(VERSION) -X main.FullVersion=$(FULLVERSION) -X main.Build=$(BUILD) -X main.WithProvisioning=OFF -X main.WithOpenSVC=OFF -X main.WithHaproxy=ON -X main.WithMaxscale=ON  -X main.WithMariadbshardproxy=ON -X  main.WithProxysql=ON -X  main.WithSphinx=ON -X main.WithArbitration=OFF -X main.WithArbitrationClient=ON -X main.WithMonitoring=ON -X main.WithHttp=ON -X main.WithBackup=ON -X main.WithMail=ON -X main.WithEnforce=ON -X main.WithDeprecate=ON"  $(LDFLAGS) -o $(BINDIR)/$(BIN-OSC-CGO)

osc-basedir:
	env CGO_ENABLED=0 GOOS=$(OS) GOARCH=amd64  go build -v --tags "netgo server" --ldflags "-extldflags 'static' -w -s $(TAR) -X main.GoOS=$(OS) -X main.GoArch=amd64 -X main.Version=$(VERSION) -X main.FullVersion=$(FULLVERSION) -X main.Build=$(BUILD) -X main.WithProvisioning=OFF -X main.WithOpenSVC=OFF -X main.WithHaproxy=ON -X main.WithMaxscale=ON  -X main.WithMariadbshardproxy=ON -X  main.WithProxysql=ON -X  main.WithSphinx=ON -X main.WithArbitration=OFF -X main.WithArbitrationClient=ON -X main.WithMonitoring=ON -X main.WithHttp=ON -X main.WithBackup=ON -X main.WithMail=ON -X main.WithEnforce=ON -X main.WithDeprecate=ON"  $(LDFLAGS) -o $(BINDIR)/$(BIN-OSC)-basedir

tst:
	env GOOS=$(OS) GOARCH=amd64  go build -v --tags "netgo server" --ldflags "-w -s -X main.GoOS=$(OS) -X main.GoArch=amd64 -X main.Version=$(VERSION) -X main.FullVersion=$(FULLVERSION) -X main.Build=$(BUILD) -X main.WithProvisioning=ON -X main.WithOpenSVC=OFF -X main.WithHaproxy=ON -X main.WithMaxscale=ON  -X main.WithMariadbshardproxy=ON -X  main.WithProxysql=ON -X  main.WithSphinx=ON -X main.WithArbitration=OFF -X main.WithArbitrationClient=ON  -X main.WithMonitoring=ON -X main.WithHttp=ON -X main.WithBackup=ON -X main.WithMail=ON -X main.WithEnforce=ON -X main.WithDeprecate=OFF"  $(LDFLAGS) -o $(BINDIR)/$(BIN-TST)

tst-basedir:
	env GOOS=$(OS) GOARCH=amd64  go build -v --tags "netgo server" --ldflags "-w -s $(TAR) -X main.GoOS=$(OS) -X main.GoArch=amd64 -X main.Version=$(VERSION) -X main.FullVersion=$(FULLVERSION) -X main.Build=$(BUILD) -X main.WithProvisioning=ON -X main.WithOpenSVC=OFF -X main.WithHaproxy=ON -X main.WithMaxscale=ON  -X main.WithMariadbshardproxy=ON -X  main.WithProxysql=ON -X  main.WithSphinx=ON -X main.WithArbitration=OFF -X main.WithArbitrationClient=ON  -X main.WithMonitoring=ON -X main.WithHttp=ON -X main.WithBackup=ON -X main.WithMail=ON -X main.WithEnforce=ON -X main.WithDeprecate=OFF"  $(LDFLAGS) -o $(BINDIR)/$(BIN-TST)-basedir

pro:
	env GOOS=$(OS) GOARCH=amd64  go build -v --tags "netcgo server" --ldflags "-w -s -X main.GoOS=$(OS) -X main.GoArch=amd64 -X main.Version=$(VERSION) -X main.FullVersion=$(FULLVERSION) -X main.Build=$(BUILD) -X main.WithProvisioning=ON -X main.WithOpenSVC=ON -X main.WithHaproxy=ON -X main.WithMaxscale=ON  -X main.WithMariadbshardproxy=ON -X  main.WithProxysql=ON -X  main.WithSphinx=ON -X main.WithArbitration=OFF -X main.WithArbitrationClient=ON  -X main.WithMonitoring=ON -X main.WithHttp=ON -X main.WithBackup=ON -X main.WithMail=ON -X main.WithEnforce=ON -X main.WithDeprecate=ON"  $(LDFLAGS) -o $(BINDIR)/$(BIN-PRO)

pro-basedir:
	env GOOS=$(OS) GOARCH=amd64  go build -v --tags "netgo server" --ldflags "-w -s $(TAR) -X main.GoOS=$(OS) -X main.GoArch=amd64 -X main.Version=$(VERSION) -X main.FullVersion=$(FULLVERSION) -X main.Build=$(BUILD) -X main.WithProvisioning=ON -X main.WithOpenSVC=ON -X main.WithHaproxy=ON -X main.WithMaxscale=ON  -X main.WithMariadbshardproxy=ON -X  main.WithProxysql=ON -X  main.WithSphinx=ON -X main.WithArbitration=OFF -X main.WithArbitrationClient=ON  -X main.WithMonitoring=ON -X main.WithHttp=ON -X main.WithBackup=ON -X main.WithMail=ON -X main.WithEnforce=ON -X main.WithDeprecate=ON"  $(LDFLAGS) -o $(BINDIR)/$(BIN-PRO)-basedir

min:
	env CGO_ENABLED=0 GOOS=$(OS) GOARCH=amd64  go build -v --tags "netgo server" --ldflags "-extldflags 'static' -w -s -X main.GoOS=$(OS) -X main.GoArch=amd64 -X main.Version=$(VERSION) -X main.FullVersion=$(FULLVERSION) -X main.Build=$(BUILD) -X main.WithProvisioning=OFF -X main.WithOpenSVC=OFF -X main.WithHaproxy=OFF -X main.WithMaxscale=OFF  -X main.WithMariadbshardproxy=OFF -X  main.WithProxysql=OFF -X main.WithArbitration=OFF -X main.WithArbitrationClient=OFF  -X main.WithMonitoring=OFF -X main.WithHttp=OFF -X main.WithMail=ON -X main.WithEnforce=OFF -X main.WithDeprecate=OFF"  $(LDFLAGS) -o $(BINDIR)/$(BIN-MIN)

min-basedir:
	env CGO_ENABLED=0 GOOS=$(OS) GOARCH=amd64  go build -v --tags "netgo server" --ldflags "-extldflags 'static' -w -s $(TAR) -X main.GoOS=$(OS) -X main.GoArch=amd64 -X main.Version=$(VERSION) -X main.FullVersion=$(FULLVERSION) -X main.Build=$(BUILD) -X main.WithProvisioning=OFF -X main.WithOpenSVC=OFF -X main.WithHaproxy=OFF -X main.WithMaxscale=OFF  -X main.WithMariadbshardproxy=OFF -X  main.WithProxysql=OFF -X main.WithArbitration=OFF -X main.WithArbitrationClient=OFF  -X main.WithMonitoring=OFF -X main.WithHttp=OFF -X main.WithMail=ON -X main.WithEnforce=OFF -X main.WithDeprecate=OFF"  $(LDFLAGS) -o $(BINDIR)/$(BIN-MIN)-basedir

cli:
	env GOOS=$(OS) GOARCH=amd64  go build -v --tags "netgo clients" --ldflags "-w -s -X main.GoOS=$(OS) -X main.GoArch=amd64 -X main.Version=$(VERSION) -X main.FullVersion=$(FULLVERSION) -X main.Build=$(BUILD) -X main.WithProvisioning=ON -X main.WithOpenSVC=ON -X main.WithHaproxy=ON -X main.WithMaxscale=ON  -X main.WithMariadbshardproxy=ON -X  main.WithProxysql=ON -X  main.WithSphinx=ON -X main.WithArbitration=OFF -X main.WithArbitrationClient=OFF  -X main.WithMonitoring=ON -X main.WithHttp=ON -X main.WithBackup=ON -X main.WithMail=ON -X main.WithEnforce=ON -X main.WithDeprecate=ON"  $(LDFLAGS) -o $(BINDIR)/$(BIN-CLI)

arb:
	env GOOS=$(OS) GOARCH=amd64  go build -v --tags "netgo arbitrator" --ldflags "-w -s -X main.GoOS=$(OS) -X main.GoArch=amd64 -X main.Version=$(VERSION) -X main.FullVersion=$(FULLVERSION) -X main.Build=$(BUILD) -X main.WithProvisioning=ON -X main.WithOpenSVC=ON -X main.WithHaproxy=ON -X main.WithMaxscale=ON  -X main.WithMariadbshardproxy=ON -X  main.WithProxysql=ON -X  main.WithSphinx=ON -X main.WithArbitration=ON -X main.WithMonitoring=ON -X main.WithHttp=ON -X main.WithBackup=OFF -X main.WithMail=ON -X main.WithEnforce=ON -X main.WithDeprecate=ON"  $(LDFLAGS) -o $(BINDIR)/$(BIN-ARB)

package: all
	nobuild=0 ./package_$(OS)_amd64.sh

clean:
	find $(BINDIR) -type f | xargs rm
