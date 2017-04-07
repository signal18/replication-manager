// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Authors: Guillaume Lefranc <guillaume@signal18.io>
//          Stephane Varoqui  <stephane@mariadb.com>
// This source code is licensed under the GNU General Public License, version 3.

package dbhelper

import (
	"fmt"
	"runtime"
	"sort"
	"time"

	"github.com/jmoiron/sqlx"
)

type driver struct {
	name string
	db   *sqlx.DB
}

type Result struct {
	Err      error
	Queries  int
	Duration time.Duration
	Allocs   uint64
	Bytes    uint64
}

func (res *Result) QueriesPerSecond() float64 {
	return float64(res.Queries) / res.Duration.Seconds()
}

func (res *Result) AllocsPerQuery() int {
	return int(res.Allocs) / res.Queries
}

func (res *Result) BytesPerQuery() int {
	return int(res.Bytes) / res.Queries
}

var memStats runtime.MemStats

type benchmark struct {
	name string
	n    int
	bm   func(*sqlx.DB, int) error
}

func (b *benchmark) run(db *sqlx.DB) Result {
	runtime.GC()

	runtime.ReadMemStats(&memStats)
	var (
		startMallocs    = memStats.Mallocs
		startTotalAlloc = memStats.TotalAlloc
		startTime       = time.Now()
	)

	err := b.bm(db, b.n)

	endTime := time.Now()
	runtime.ReadMemStats(&memStats)

	return Result{
		Err:      err,
		Queries:  b.n,
		Duration: endTime.Sub(startTime),
		Allocs:   memStats.Mallocs - startMallocs,
		Bytes:    memStats.TotalAlloc - startTotalAlloc,
	}
}

type BenchmarkSuite struct {
	drivers     []driver
	benchmarks  []benchmark
	WarmUp      func(*sqlx.DB) error
	Repetitions int
	PrintStats  bool
}

func (bs *BenchmarkSuite) AddDriver(name, drv, dsn string) error {
	db, err := sqlx.Open(drv, dsn)
	if err != nil {
		return fmt.Errorf("Error registering driver '%s': %s", name, err.Error())
	}

	if err = db.Ping(); err != nil {
		return fmt.Errorf("Error on driver '%s': %s", name, err.Error())
	}

	bs.drivers = append(bs.drivers, driver{
		name: name,
		db:   db,
	})
	return nil
}

func (bs *BenchmarkSuite) AddBenchmark(name string, n int, bm func(*sqlx.DB, int) error) {
	bs.benchmarks = append(bs.benchmarks, benchmark{
		name: name,
		n:    n,
		bm:   bm,
	})
}

func (bs *BenchmarkSuite) Run() string {
	startTime := time.Now()

	if len(bs.drivers) < 1 {
		return "No drivers registered to run benchmarks with!"
	}

	if len(bs.benchmarks) < 1 {
		return "No benchmark functions registered!"
	}

	if bs.WarmUp != nil {
		for _, driver := range bs.drivers {
			fmt.Println("Warming up " + driver.name + "...")
			if err := bs.WarmUp(driver.db); err != nil {

				return err.Error()
			}
		}
		fmt.Println()
	}

	var qps []float64
	if bs.Repetitions > 1 && bs.PrintStats {
		qps = make([]float64, bs.Repetitions)
	} else {
		bs.PrintStats = false
	}
	back := ""
	for _, benchmark := range bs.benchmarks {
		back = back + fmt.Sprintln(benchmark.name, benchmark.n, "iterations")
		for _, driver := range bs.drivers {
			for i := 0; i < bs.Repetitions; i++ {
				res := benchmark.run(driver.db)
				if res.Err != nil {
					back = back + fmt.Sprintln(res.Err.Error())
				} else {
					back = back + fmt.Sprintln(
						" "+
							res.Duration.String(), "\t   ",
						int(res.QueriesPerSecond()+0.5), "queries/sec\t   ",
						res.AllocsPerQuery(), "allocs/query\t   ",
						res.BytesPerQuery(), "B/query",
					)
					if bs.Repetitions > 1 {
						qps[i] = res.QueriesPerSecond()
					}
				}
			}

			if bs.PrintStats {
				var totalQps float64
				for i := range qps {
					totalQps += qps[i]
				}

				sort.Float64s(qps)

				back = back + fmt.Sprintln(
					" -- "+
						"avg", int(totalQps/float64(len(qps))+0.5), "qps;  "+
						"median", int(qps[len(qps)/2]+0.5), "qps",
				)
			}
		}

		back = back + fmt.Sprintln()
	}
	endTime := time.Now()
	back = back + fmt.Sprintln("Finished... Total running time:", endTime.Sub(startTime).String())
	return back
}
