package splitdump

import (
	"bufio"
	"io"
	"regexp"
	"sync"
)

// DefinerStripRe matches DEFINER=`user`@`host` clauses in SQL output.
// It is exported so all restore paths (CLI, server) share identical stripping semantics.
var DefinerStripRe = regexp.MustCompile("(?i)DEFINER\\s*=\\s*`[^`]+`@`[^`]+`")

// NewDefinerStrippingReader returns a reader that transparently strips DEFINER=`user`@`host`
// clauses from every line in src. It uses bufio.Reader.ReadString so lines of arbitrary
// length (including multi-megabyte INSERT rows) are handled without error — unlike
// bufio.Scanner which returns bufio.ErrTooLong when its fixed token buffer is exceeded.
//
// A background goroutine performs the stripping. The caller MUST call done after consuming
// the reader (or abandoning it on error) and BEFORE any deferred Close on the underlying
// source. Passing nil to done signals clean completion; passing a non-nil error propagates
// it. done blocks until the goroutine has fully exited, making it safe to close src.
func NewDefinerStrippingReader(src io.Reader) (r io.Reader, done func(error)) {
	pr, pw := io.Pipe()
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		br := bufio.NewReaderSize(src, 64*1024)
		for {
			line, err := br.ReadString('\n')
			if line != "" {
				stripped := DefinerStripRe.ReplaceAllString(line, "")
				if _, writeErr := pw.Write([]byte(stripped)); writeErr != nil {
					return // pipe read-end closed; discard remaining input
				}
			}
			if err != nil {
				if err == io.EOF {
					pw.Close()
				} else {
					pw.CloseWithError(err)
				}
				return
			}
		}
	}()
	return pr, func(err error) {
		_ = pr.CloseWithError(err) // unblock goroutine if blocked in pw.Write
		wg.Wait()                  // wait until goroutine exits before src is closed
	}
}
