# g2g

Get to Graphite: publish Go expvars to a Graphite server.

[![Build Status][1]][2]

[1]: https://secure.travis-ci.org/peterbourgon/g2g.png
[2]: http://www.travis-ci.org/peterbourgon/g2g

**See also** [g2s: Get to Statsd](https://github.com/peterbourgon/g2s), to emit
statistics to a Statsd server.

# Usage

The assumption is that the information you want to get into Graphite is already
an expvar in your Go program. So, if that's not the case, register yourself
some expvars. (It's not necessary to boot up an HTTP server if you don't want
one.) For example,

```go
var (
	loadedRecords = expvar.NewInt("records loaded into the player")
)

func LoadThemAll() {
	a := getSomeRecords()
	for _, x := range a {
		load(x)
	}
	go loadedRecords.Add(int64(len(a)))
}
```

Now, at whatever scope makes sense (probably in your main function), create
a Graphite object, and Register your variables.

```go
func main() {

	// ...

	interval := 30 * time.Second
	timeout := 3 * time.Second
	g := g2g.NewGraphite("graphite-server:2003", interval, timeout)
	g.Register("foo.service.records.loaded", loadedRecords)

	// ...
}
```

Now, every `interval` time-period, all Registered expvars will be published to
the Graphite server.

Operations on the Graphite structure are goroutine-safe.
