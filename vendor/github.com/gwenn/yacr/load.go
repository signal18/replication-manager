// +build ignore

package main

import (
	"flag"
	"fmt"
	"log"
	"os"
	"runtime/pprof"

	"github.com/gwenn/yacr"
)

var cpuprofile = flag.String("cpuprofile", "", "write cpu profile to file")

// http://download.geonames.org/export/dump/allCountries.zip
func main() {
	flag.Parse()
	if *cpuprofile != "" {
		f, err := os.Create(*cpuprofile)
		if err != nil {
			log.Fatal(err)
		}
		pprof.StartCPUProfile(f)
		defer pprof.StopCPUProfile()
	}

	r := yacr.NewReader(os.Stdin, '\t', false, false)
	w := yacr.NewWriter(os.Stdout, '\t', false)
	for r.Scan() && w.Write(r.Bytes()) {
		if r.EndOfRecord() {
			w.EndOfRecord()
		}
	}
	w.Flush()
	if err := r.Err(); err != nil {
		fmt.Fprintln(os.Stderr, err)
	}
	if err := w.Err(); err != nil {
		fmt.Fprintln(os.Stderr, err)
	}
}
