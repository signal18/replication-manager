// functions.go
package common

import (
	"fmt"
	"os"
	"strconv"
)

func Version() {
	fmt.Println("MariaDB Tools version 0.0.1")
	os.Exit(0)
}

func DrawHashline(t string, l int) string {
	var hashline string
	hashline = "### " + t + " "
	l = l - len(hashline)
	for i := 0; i <= l; i++ {
		hashline = hashline + "#"
	}
	return hashline
}

func DecimaltoPct(q float64, d float64) int {
	return int(((q / d) * 100) + 0.5)
}

func DecimaltoPctLow(q float64, d float64) int {
	return int(((1 - (q / d)) * 100) + 0.5)
}

func StrtoUint(s string) uint64 {
	u, _ := strconv.ParseUint(s, 10, 64)
	return u
}

func StrtoInt(s string) int64 {
	u, _ := strconv.ParseInt(s, 10, 64)
	return u
}

func StrtoFloat(s string) float64 {
	u, _ := strconv.ParseFloat(s, 64)
	return u
}
