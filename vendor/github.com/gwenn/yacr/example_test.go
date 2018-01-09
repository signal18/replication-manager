package yacr_test

import (
	"fmt"
	"os"
	"strings"

	yacr "github.com/gwenn/yacr"
)

func Example() {
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

func Example_reader() {
	r := yacr.DefaultReader(strings.NewReader("c1,\"c\"\"2\",\"c\n3\",\"c,4\""))
	fmt.Print("[")
	for r.Scan() {
		fmt.Print(r.Text())
		if r.EndOfRecord() {
			fmt.Print("]\n")
		} else {
			fmt.Print(" ")
		}
	}
	if err := r.Err(); err != nil {
		fmt.Println(err)
	}
	// Output: [c1 c"2 c
	// 3 c,4]
}

func ExampleReader_Value() {
	r := yacr.DefaultReader(strings.NewReader("1,\"2\",3,4"))
	fmt.Print("[")
	var i int
	for r.Scan() {
		if err := r.Value(&i); err != nil {
			fmt.Println(err)
			break
		}
		fmt.Print(i)
		if r.EndOfRecord() {
			fmt.Print("]\n")
		} else {
			fmt.Print(" ")
		}
	}
	if err := r.Err(); err != nil {
		fmt.Println(err)
	}
	// Output: [1 2 3 4]
}

func ExampleReader_ScanRecord() {
	r := yacr.DefaultReader(strings.NewReader("11,12,13,14\n21,22,23,24\n31,32,33,34\n41,42,43,44"))
	fmt.Print("[")
	var i1, i2, i3, i4 int
	for {
		if n, err := r.ScanRecord(&i1, &i2, &i3, &i4); err != nil {
			fmt.Println(err)
			break
		} else if n != 4 {
			break
		}
		fmt.Println(i1, i2, i3, i4)
	}
	fmt.Print("]")
	// Output: [11 12 13 14
	// 21 22 23 24
	// 31 32 33 34
	// 41 42 43 44
	// ]
}

func Example_writer() {
	w := yacr.DefaultWriter(os.Stdout)
	for _, field := range []string{"c1", "c\"2", "c\n3", "c,4"} {
		if !w.WriteString(field) {
			break
		}
	}
	w.Flush()
	if err := w.Err(); err != nil {
		fmt.Fprintln(os.Stderr, err)
	}
	// Output: c1,"c""2","c
	// 3","c,4"
}
