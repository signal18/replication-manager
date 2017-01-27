.PHONY: all cairo example

cairo: cairo/cairo.go cairo/*.go
	go install github.com/martine/gocairo/cairo

all: cairo example

example: cairo example/*
	go run example/basic.go
	go run example/error.go
	go run example/lines.go
	go run example/path.go

cairo/cairo.go: gen.go fake-xlib.h
	go run gen.go > $@
