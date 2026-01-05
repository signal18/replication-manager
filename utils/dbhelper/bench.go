// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2021 SIGNAL18 CLOUD SAS
// Authors: Guillaume Lefranc <guillaume@signal18.io>
//          Stephane Varoqui  <svaroqui@gmail.com>
// This source code is licensed under the GNU General Public License, version 3.

// This file contains database connection benchmarking utilities.
// It provides tools for testing and measuring database connection performance
// across different drivers and configurations.

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

// ChecksumTable performs CHECKSUM TABLE on the given table
func ChecksumTable(db *sqlx.DB, table string) (string, error) {
	// Validate table name to prevent SQL injection
	if err := ValidateIdentifier(table); err != nil {
		return "", fmt.Errorf("invalid table name: %w", err)
	}

	var tableres string
	var checkres string
	query := "CHECKSUM TABLE " + QuoteMySQLIdentifier(table) + " EXTENDED"
	err := db.QueryRowx(query).Scan(&tableres, &checkres)
	return checkres, err
}

// InjectLongTrx injects a long-running transaction for testing
func InjectLongTrx(db *sqlx.DB, time int) error {
	if err := benchWarmup(db); err != nil {
		return err
	}

	_, err := db.Exec("START TRANSACTION")
	if err != nil {
		return err
	}

	_, err = db.Exec("INSERT INTO replication_manager_schema.bench(val) VALUES(1)")
	if err != nil {
		return err
	}

	_, err = db.Exec(fmt.Sprintf("SELECT SLEEP(%d)", time))
	if err != nil {
		return err
	}

	_, err = db.Exec("COMMIT")
	return err
}

// InjectTrxWithoutCommit injects a transaction without committing for testing
func InjectTrxWithoutCommit(db *sqlx.DB) error {
	if err := benchWarmup(db); err != nil {
		return err
	}

	_, err := db.Exec("START TRANSACTION")
	if err != nil {
		return err
	}

	_, err = db.Exec("INSERT INTO replication_manager_schema.bench(val) VALUES(1)")
	return err
}

// WriteConcurrent2 performs concurrent write benchmarks
func WriteConcurrent2(dsn string, qt int) (string, error) {
	bs := BenchmarkSuite{
		WarmUp:      benchWarmup,
		Repetitions: 1,
		PrintStats:  true,
	}

	if err := bs.AddDriver("mysql", "mysql", dsn); err != nil {
		return "", err
	}

	bs.AddBenchmark("PreparedExecConcurrent2", qt, benchPreparedExecConcurrent2)

	result := bs.Run()
	return result, nil
}

// BenchCleanup cleans up benchmark tables
func BenchCleanup(db *sqlx.DB) error {
	_, err := db.Exec("DROP TABLE IF EXISTS replication_manager_schema.bench")
	if err != nil {
		return err
	}
	_, err = db.Exec("DROP DATABASE IF EXISTS replication_manager_schema")
	return err
}

// benchWarmup prepares the database for benchmarking
func benchWarmup(db *sqlx.DB) error {
	db.SetMaxIdleConns(16)
	_, err := db.Exec("CREATE DATABASE IF NOT EXISTS replication_manager_schema")
	if err != nil {
		return err
	}
	_, err = db.Exec("DROP TABLE IF EXISTS replication_manager_schema.bench")
	if err != nil {
		return err
	}
	_, err = db.Exec("CREATE TABLE IF NOT EXISTS replication_manager_schema.bench(id bigint unsigned primary key auto_increment, val bigint unsigned)")
	if err != nil {
		return err
	}
	_, err = db.Exec("INSERT INTO replication_manager_schema.bench(val) VALUES(1)")
	if err != nil {
		return err
	}

	for i := 0; i < 2; i++ {
		rows, err := db.Query("SELECT val FROM replication_manager_schema.bench")
		if err != nil {
			return err
		}

		if err = rows.Close(); err != nil {
			return err
		}
	}
	return nil
}

// benchPreparedExecConcurrent2 is the actual benchmark function
func benchPreparedExecConcurrent2(db *sqlx.DB, _ int) error {
	stmt, err := db.Prepare("INSERT INTO replication_manager_schema.bench(val) VALUES(?)")
	if err != nil {
		return err
	}
	defer stmt.Close()

	for i := 0; i < 100; i++ {
		_, err = stmt.Exec(i)
		if err != nil {
			return err
		}
	}
	return nil
}
