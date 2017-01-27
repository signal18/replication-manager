// mini logging package
package mlog

import (
	"io"
	"log"
	"os"

	"github.com/lestrrat/go-file-rotatelogs"
)

type Level int

var output io.Writer

const (
	Normal Level = iota
	Debug
	Trace
)

func setOutputWriter(writer io.Writer) {
	output = writer
	log.SetOutput(writer)
}

func GetOutput() io.Writer {
	return output
}

const strftimeFormat = ".%Y%m%d%H%M.log"

func SetOutput(logdir, service string, stdout bool) {
	rl, _ := rotatelogs.New(
		logdir+"/"+service+strftimeFormat,
		rotatelogs.WithClock(rotatelogs.Local),
		rotatelogs.WithLinkName(logdir+"/"+service+".log"),
	)

	if stdout {
		setOutputWriter(io.MultiWriter(os.Stdout, rl))
	} else {
		setOutputWriter(rl)
	}
}

func SetRawStream(w io.Writer) {
	log.SetFlags(0)
	setOutputWriter(w)
}

func (ll Level) Debugf(format string, a ...interface{}) {
	if ll >= Debug {
		log.Printf(format, a...)
	}
}

func (ll Level) Debugln(a ...interface{}) {
	if ll >= Debug {
		log.Println(a...)
	}
}

func (ll Level) Tracef(format string, a ...interface{}) {
	if ll >= Trace {
		log.Printf(format, a...)
	}
}

func (ll Level) Traceln(a ...interface{}) {
	if ll >= Trace {
		log.Println(a...)
	}
}
func (ll Level) Logln(a ...interface{}) {
	log.Println(a...)
}

func (ll Level) Logf(format string, a ...interface{}) {
	log.Printf(format, a...)
}

func (ll Level) Fatalf(format string, a ...interface{}) {
	log.Printf(format, a...)
	os.Exit(1)
}

func (ll Level) Fatalln(a ...interface{}) {
	log.Println(a...)
	os.Exit(1)
}
