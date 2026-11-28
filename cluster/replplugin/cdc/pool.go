// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2026 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

package cdc

import (
	"context"
	"fmt"
	"sort"
	"sync"
	"time"
)

// The pool of the per-partition work (CDC_CORE.md "Partitioning"): the jobs of one round, one
// per partition, run on a bounded number of workers (wire.Config.Threads), never two on one
// partition, each under its deadline. A failed partition does not stop the others: its error is
// returned by name, the caller keeps its work for the next round and says which partition and
// why (F7). The init's apply uses it; the S3 destination's partition exchange is its next user.

const (
	defaultThreads = 2
	maxThreads     = 64
)

// threadsOf is the workers of n threads: 0 the default, capped.
func threadsOf(n int) int {
	if n <= 0 {
		return defaultThreads
	}
	return min(n, maxThreads)
}

// runPartitions runs jobs (partition -> its job) on threads workers, each job under deadline
// (0: none), and returns the error of each failed partition. A job not started when ctx ends
// fails with ctx's error.
func runPartitions(ctx context.Context, threads int, deadline time.Duration, jobs map[string]func(context.Context) error) map[string]error {
	names := make([]string, 0, len(jobs))
	for n := range jobs {
		names = append(names, n)
	}
	sort.Strings(names)
	errs := map[string]error{}
	var mu sync.Mutex
	var wg sync.WaitGroup
	sem := make(chan struct{}, threadsOf(threads))
	for _, n := range names {
		select {
		case sem <- struct{}{}:
		case <-ctx.Done():
			mu.Lock()
			errs[n] = ctx.Err()
			mu.Unlock()
			continue
		}
		wg.Add(1)
		go func(n string, job func(context.Context) error) {
			defer wg.Done()
			defer func() { <-sem }()
			jctx, cancel := ctx, context.CancelFunc(func() {})
			if deadline > 0 {
				jctx, cancel = context.WithTimeout(ctx, deadline)
			}
			defer cancel()
			err := func() (err error) {
				defer func() {
					if p := recover(); p != nil {
						err = fmt.Errorf("panic: %v", p)
					}
				}()
				return job(jctx)
			}()
			if err != nil {
				mu.Lock()
				errs[n] = err
				mu.Unlock()
			}
		}(n, jobs[n])
	}
	wg.Wait()
	return errs
}

// partitionErrors says the failed partitions, in order.
func partitionErrors(errs map[string]error) error {
	if len(errs) == 0 {
		return nil
	}
	names := make([]string, 0, len(errs))
	for n := range errs {
		names = append(names, n)
	}
	sort.Strings(names)
	msg := ""
	for i, n := range names {
		if i > 0 {
			msg += "; "
		}
		label := n
		if label == "" {
			label = "(not partitioned)"
		}
		msg += "partition " + label + ": " + errs[n].Error()
	}
	return fmt.Errorf("%s", msg)
}
