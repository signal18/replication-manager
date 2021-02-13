#!/usr/bin/env sh
# These are the values we want to pass for VERSION and BUILD
# git tag 1.0.1
# git commit -am "One more change after the tags"
set -e
VERSION=$(git describe --abbrev=0 --tags)
FULLVERSION=$(git describe --tags)
BUILD=$(date +%FT%T%z)

TAR="-X main.WithTarball=ON"
BINARY=replication-manager-osc
env CGO_ENABLED=0 GOOS=linux GOARCH=amd64  go build -a -v --tags "netgo server" --ldflags "-extldflags 'static' -w -s -X main.GoOS=linux -X main.GoArch=amd64 -X main.Version=${VERSION} -X main.FullVersion=${FULLVERSION} -X main.Build=${BUILD} -X main.WithProvisioning=OFF "  ${LDFLAGS} -o ${BINARY}
BINARY=replication-manager-osc-basedir
env CGO_ENABLED=0 GOOS=linux GOARCH=amd64  go build -a -v --tags "netgo server" --ldflags "-extldflags 'static' -w -s $TAR -X main.GoOS=linux -X main.GoArch=amd64 -X main.Version=${VERSION} -X main.FullVersion=${FULLVERSION} -X main.Build=${BUILD} -X main.WithProvisioning=OFF "  ${LDFLAGS} -o ${BINARY}


BINARY=replication-manager-tst
env GOOS=linux GOARCH=amd64  go build -a -v --tags "netgo server" --ldflags "-w -s -X main.GoOS=linux -X main.GoArch=amd64 -X main.Version=${VERSION} -X main.FullVersion=${FULLVERSION} -X main.Build=${BUILD}   -X main.WithDeprecate=OFF"  ${LDFLAGS} -o ${BINARY}
BINARY=replication-manager-tst-basedir
env GOOS=linux GOARCH=amd64  go build -a -v --tags "netgo server" --ldflags "-w -s $TAR -X main.GoOS=linux -X main.GoArch=amd64 -X main.Version=${VERSION} -X main.FullVersion=${FULLVERSION} -X main.Build=${BUILD}   -X main.WithDeprecate=OFF"  ${LDFLAGS} -o ${BINARY}

BINARY=replication-manager-pro
env GOOS=linux GOARCH=amd64  go build -a -v --tags "netgo server" --ldflags "-w -s -X main.GoOS=linux -X main.GoArch=amd64 -X main.Version=${VERSION} -X main.FullVersion=${FULLVERSION} -X main.Build=${BUILD} -X main.WithOpenSVC=ON  "  ${LDFLAGS} -o ${BINARY}
BINARY=replication-manager-pro-basedir
env GOOS=linux GOARCH=amd64  go build -a -v --tags "netgo server" --ldflags "-w -s $TAR -X main.GoOS=linux -X main.GoArch=amd64 -X main.Version=${VERSION} -X main.FullVersion=${FULLVERSION} -X main.Build=${BUILD} -X main.WithOpenSVC=ON  "  ${LDFLAGS} -o ${BINARY}

BINARY=replication-manager-min
env CGO_ENABLED=0 GOOS=linux GOARCH=amd64  go build -a -v --tags "netgo server" --ldflags "-extldflags 'static' -w -s -X main.GoOS=linux -X main.GoArch=amd64 -X main.Version=${VERSION} -X main.FullVersion=${FULLVERSION} -X main.Build=${BUILD} -X main.WithProvisioning=OFF -X main.WithHaproxy=OFF -X main.WithMaxscale=OFF  -X main.WithMariadbshardproxy=OFF -X  main.WithProxysql=OFF -X main.WithArbitrationClient=OFF  -X main.WithMonitoring=OFF -X main.WithHttp=OFF -X main.WithEnforce=OFF -X main.WithDeprecate=OFF"  ${LDFLAGS} -o ${BINARY}
BINARY=replication-manager-min-basedir
env CGO_ENABLED=0 GOOS=linux GOARCH=amd64  go build -a -v --tags "netgo server" --ldflags "-extldflags 'static' -w -s $TAR -X main.GoOS=linux -X main.GoArch=amd64 -X main.Version=${VERSION} -X main.FullVersion=${FULLVERSION} -X main.Build=${BUILD} -X main.WithProvisioning=OFF -X main.WithHaproxy=OFF -X main.WithMaxscale=OFF  -X main.WithMariadbshardproxy=OFF -X  main.WithProxysql=OFF -X main.WithArbitrationClient=OFF  -X main.WithMonitoring=OFF -X main.WithHttp=OFF -X main.WithEnforce=OFF -X main.WithDeprecate=OFF"  ${LDFLAGS} -o ${BINARY}

BINARY=replication-manager-cli
env GOOS=linux GOARCH=amd64  go build -a -v --tags "netgo clients" --ldflags "-w -s -X main.GoOS=linux -X main.GoArch=amd64 -X main.Version=${VERSION} -X main.FullVersion=${FULLVERSION} -X main.Build=${BUILD} -X main.WithOpenSVC=ON  -X main.WithArbitrationClient=OFF "  ${LDFLAGS} -o ${BINARY}

BINARY=replication-manager-arb
env GOOS=linux GOARCH=amd64  go build -a -v --tags "netgo arbitrator" --ldflags "-w -s -X main.GoOS=linux -X main.GoArch=amd64 -X main.Version=${VERSION} -X main.FullVersion=${FULLVERSION} -X main.Build=${BUILD} -X main.WithOpenSVC=ON  -X main.WithArbitration=ON"  ${LDFLAGS} -o ${BINARY}

#BINARY=mrm-test
#env GOOS=linux GOARCH=amd64  go build -a -v --tags "netgo server" --ldflags "-w -s -X main.GoOS=linux -X main.GoArch=amd64 -X main.Version=${VERSION} -X main.FullVersion=${FULLVERSION} -X main.Build=${BUILD} -X main.WithOpenSVC=ON -X main.WithHaproxy=OFF -X main.WithMaxscale=OFF  -X main.WithMariadbshardproxy=OFF -X  main.WithProxysql=OFF -X main.WithMonitoring=OFF -X main.WithHttp=OFF -X main.WithEnforce=OFF -X main.WithDeprecate=OFF"  ${LDFLAGS} -o ${BINARY}

#BINARY=mrm-cli
#env GOOS=linux GOARCH=amd64  go build -a -v --tags "netgo clients" --ldflags "-w -s -X main.GoOS=linux -X main.GoArch=amd64 -X main.Version=${VERSION} -X main.FullVersion=${FULLVERSION} -X main.Build=${BUILD} -X main.WithOpenSVC=ON -X main.WithHaproxy=OFF -X main.WithMaxscale=OFF  -X main.WithMariadbshardproxy=OFF -X  main.WithProxysql=OFF -X main.WithMonitoring=OFF -X main.WithHttp=OFF -X main.WithEnforce=OFF -X main.WithDeprecate=OFF"  ${LDFLAGS} -o ${BINARY}
