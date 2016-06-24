// Package termlog
// termbox logging package
package termlog

import (
	"time"

	"github.com/nsf/termbox-go"
)

type TermLog struct {
	Buffer []string
	Len    int
	Line   int
}

func NewTermLog(sz int) TermLog {
	tl := TermLog{}
	tl.Len = sz
	tl.Buffer = make([]string, tl.Len)
	return tl
}

func (tl *TermLog) Add(s string) {
	ts := time.Now().Format("2006-01-02 15:04:05")
	s = " " + ts + " " + s
	tl.Shift(s)
}

func (tl *TermLog) Shift(e string) {
	ns := make([]string, 1)
	ns[0] = e
	tl.Buffer = append(ns, tl.Buffer[0:tl.Len]...)
}

func (tl *TermLog) Extend() {
	tl.Buffer = append(tl.Buffer, make([]string, tl.Len)...)
}

func (tl *TermLog) Shrink() {
	tl.Buffer = tl.Buffer[:tl.Len]
}

func (tl TermLog) Print() {
	for _, line := range tl.Buffer {
		x := 0
		for _, c := range line {
			termbox.SetCell(x, tl.Line, c, termbox.ColorWhite, termbox.ColorBlack)
			x++
		}
		tl.Line++
	}
}
