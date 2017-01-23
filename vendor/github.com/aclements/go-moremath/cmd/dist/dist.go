// dist reads newline-separated numbers and describes their distribution.
//
// For example,
//
//  $ seq 1 20 | grep -v 1 | dist
//  N 9  sum 64  mean 7.11111  gmean 5.78509  std dev 5.34894  variance 28.6111
//
//       min 2
//     1%ile 2
//     5%ile 2
//    25%ile 3.66667
//    median 6
//    75%ile 8.33333
//    95%ile 20
//    99%ile 20
//       max 20
//
//  ⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⣀⣠⠖⠒⠒⠒⠒⠒⠒⠒⠒⠒⠒⠒⠒⠒⠦⣀⡀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⡖ 0.1
//  ⠀⠀⠀⠀⠀⠀⠀⢀⣠⠴⠊⠁⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠉⠲⢤⣀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⢀⣀⣀⡀⠀⠀⠀⠀⠀⠀⠀⠀⠀⡇
//  ⠠⠤⠤⠤⠤⠴⠒⠋⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠈⠑⠲⠤⠤⠤⠤⠤⠤⠤⠤⠤⠤⠤⠤⠤⠤⠤⠤⠤⠤⠴⠒⠋⠉⠉⠀⠀⠉⠉⠙⠒⠦⠤⠤⠤⠤⠄⠧ 0.0
//  ⠈⠉⠉⠉⠉⠙⠉⠉⠉⠉⠉⠉⠉⠉⠉⠉⠉⠉⠉⠉⠉⠉⠉⠉⠉⠉⠉⠉⠉⠉⠉⠉⠋⠉⠉⠉⠉⠉⠉⠉⠉⠉⠉⠉⠉⠉⠉⠉⠉⠉⠉⠉⠉⠉⠉⠉⠉⠉⠉⠋⠉⠉⠉⠉⠉⠉⠉⠉⠉⠁
//       0                         10                         20
package main

import (
	"bufio"
	"fmt"
	"io"
	"math"
	"os"
	"strconv"
	"strings"

	"github.com/aclements/go-moremath/stats"
)

func main() {
	s := readInput(os.Stdin)
	if len(s.Xs) == 0 {
		fmt.Fprintln(os.Stderr, "no input")
		return
	}
	s.Sort()

	fmt.Printf("N %d  sum %.6g  mean %.6g", len(s.Xs), s.Sum(), s.Mean())
	gmean := s.GeoMean()
	if !math.IsNaN(gmean) {
		fmt.Printf("  gmean %.6g", gmean)
	}
	fmt.Printf("  std dev %.6g  variance %.6g\n", s.StdDev(), s.Variance())
	fmt.Println()

	// Quartiles and tails.
	labels := map[int]string{0: "min", 50: "median", 100: "max"}
	for _, p := range []int{0, 1, 5, 25, 50, 75, 95, 99, 100} {
		label, ok := labels[p]
		if !ok {
			label = fmt.Sprintf("%d%%ile", p)
		}
		fmt.Printf("%8s %.6g\n", label, s.Quantile(float64(p)/100))
	}
	fmt.Println()

	// Kernel density estimate.
	kde := &stats.KDE{Sample: s}
	FprintPDF(os.Stdout, kde)
}

func readInput(r io.Reader) (sample stats.Sample) {
	scanner := bufio.NewScanner(r)
	for scanner.Scan() {
		l := scanner.Text()
		l = strings.TrimSpace(l)
		if l == "" {
			continue
		}
		value, err := strconv.ParseFloat(l, 64)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}

		sample.Xs = append(sample.Xs, value)
	}
	if err := scanner.Err(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}

	return
}
