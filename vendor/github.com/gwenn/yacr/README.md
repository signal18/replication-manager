Yet another CSV reader (and writer) with small memory usage.

All credit goes to:
* Rob Pike, creator of Scanner interface,
* D. Richard Hipp, for his CSV parser implementation.

[![Build Status][1]][2]

[1]: https://secure.travis-ci.org/gwenn/yacr.png
[2]: http://www.travis-ci.org/gwenn/yacr

[![GoDoc](https://godoc.org/github.com/gwenn/yacr?status.svg)](https://godoc.org/github.com/gwenn/yacr)

There is a standard package named [encoding/csv](http://tip.golang.org/pkg/encoding/csv/).

<pre>
BenchmarkParsing	    5000	    381518 ns/op	 256.87 MB/s	    4288 B/op	       5 allocs/op
BenchmarkQuotedParsing	    5000	    487599 ns/op	 209.19 MB/s	    4288 B/op	       5 allocs/op
BenchmarkEmbeddedNL	    5000	    594618 ns/op	 201.81 MB/s	    4288 B/op	       5 allocs/op
BenchmarkStdParser	     500	   5026100 ns/op	  23.88 MB/s	  625499 B/op	   16037 allocs/op
BenchmarkYacrParser	    5000	    593165 ns/op	 202.30 MB/s	    4288 B/op	       5 allocs/op
BenchmarkYacrWriter	  200000	      9433 ns/op	  98.05 MB/s	    2755 B/op	       0 allocs/op
BenchmarkStdWriter	  100000	     27804 ns/op	  33.27 MB/s	    2755 B/op	       0 allocs/op
</pre>

USAGES
------
* [csvdiff](https://github.com/gwenn/csvdiff)
* [csvgrep](https://github.com/gwenn/csvgrep)
* [SQLite import/export/module](https://github.com/gwenn/gosqlite/blob/master/csv.go)