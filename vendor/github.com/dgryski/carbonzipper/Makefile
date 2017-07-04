all: carbonzipper
VERSION ?= $(shell git describe --abbrev=4 --dirty --always --tags)

GO ?= go

carbonzipper: dep
	$(GO) build --ldflags '-X main.BuildVersion=$(VERSION)'

test: dep
	$(GO) test -race
	$(GO) vet

dep:
	@which dep 2>/dev/null || $(GO) get github.com/golang/dep/cmd/dep
	dep ensure

install:
	mkdir -p $(DESTDIR)/usr/bin/
	mkdir -p $(DESTDIR)/usr/share/carbonzipper/
	cp ./carbonzipper $(DESTDIR)/usr/bin/
	cp ./example.conf $(DESTDIR)/usr/share/carbonzipper/

clean:
	rm -rf vendor
	rm -f carbonzipper
