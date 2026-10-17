/*
 * Copyright (c) 2025 Gilles Chehade <gilles@poolp.org>
 *
 * Permission to use, copy, modify, and distribute this software for any
 * purpose with or without fee is hereby granted, provided that the above
 * copyright notice and this permission notice appear in all copies.
 *
 * THE SOFTWARE IS PROVIDED "AS IS" AND THE AUTHOR DISCLAIMS ALL WARRANTIES
 * WITH REGARD TO THIS SOFTWARE INCLUDING ALL IMPLIED WARRANTIES OF
 * MERCHANTABILITY AND FITNESS. IN NO EVENT SHALL THE AUTHOR BE LIABLE FOR
 * ANY SPECIAL, DIRECT, INDIRECT, OR CONSEQUENTIAL DAMAGES OR ANY DAMAGES
 * WHATSOEVER RESULTING FROM LOSS OF USE, DATA OR PROFITS, WHETHER IN AN
 * ACTION OF CONTRACT, NEGLIGENCE OR OTHER TORTIOUS ACTION, ARISING OUT OF
 * OR IN CONNECTION WITH THE USE OR PERFORMANCE OF THIS SOFTWARE.
 */

// The bounded retry of S3 reads (5 attempts, backoff capped at 3 s), copied from
// github.com/PlakarKorp/integrations/s3 v1.1.8 common/retry.go (ISC, see
// LICENSE.plakar-integrations), renamed to unexported names.

package main

import (
	"context"
	"errors"
	"io"
	"math/rand/v2"
	"net/http"
	"time"

	"github.com/minio/minio-go/v7"
)

const (
	maxRetries = 5
)

var (
	// var for tests to be able to override
	retryBaseDelay = 200 * time.Millisecond
	retryMaxDelay  = 3 * time.Second
)

type retryReader struct {
	ctx      context.Context
	get      func(offset int64) (io.ReadCloser, error)
	expected int64 // total bytes expected, -1 when unknown
	rc       io.ReadCloser
	offset   int64
	fails    int
}

// newRetryReader streams the result of get(0) and in case of a transient
// failure during the read retries get(offset) until max retries or until
// expected||EOF
func newRetryReader(ctx context.Context, expected int64, get func(offset int64) (io.ReadCloser, error)) *retryReader {
	return &retryReader{ctx: ctx, get: get, expected: expected}
}

func (r *retryReader) Read(p []byte) (int, error) {
	for {
		// First run, or there was a failure, let's reopen it.
		if r.rc == nil {
			if r.expected >= 0 && r.offset >= r.expected {
				return 0, io.EOF
			}

			rc, err := r.get(r.offset)
			if err != nil {
				if r.pastEnd(err) {
					return 0, io.EOF
				}
				if err := r.backoff(err); err != nil {
					return 0, err
				}
				continue
			}
			r.rc = rc
		}

		n, err := r.rc.Read(p)
		r.offset += int64(n)

		if err == io.EOF && r.expected >= 0 && r.offset < r.expected {
			err = io.ErrUnexpectedEOF // short read, resume below
		}
		if err == nil || err == io.EOF {
			if n > 0 {
				r.fails = 0
			}
			return n, err
		}

		r.rc.Close()
		r.rc = nil

		if r.pastEnd(err) {
			return n, io.EOF
		}
		if n > 0 {
			// deliver what we have, resume on the next call
			r.fails = 0
			return n, nil
		}
		if err := r.backoff(err); err != nil {
			return 0, err
		}
	}
}

// pastEnd reports whether err is a 416 on a resumed read of an object of
// unknown size: the previous attempt failed right at the end of the
// object, so this is a completed read, not an error.
func (r *retryReader) pastEnd(err error) bool {
	var resp minio.ErrorResponse
	return r.expected < 0 && r.offset > 0 &&
		errors.As(err, &resp) && resp.StatusCode == http.StatusRequestedRangeNotSatisfiable
}

func (r *retryReader) backoff(err error) error {
	if !retryable(err) {
		return err
	}
	if r.fails++; r.fails > maxRetries {
		return err
	}
	delay := min(retryBaseDelay<<(r.fails-1), retryMaxDelay)
	delay += rand.N(delay/2 + 1)
	select {
	case <-r.ctx.Done():
		return r.ctx.Err()
	case <-time.After(delay):
		return nil
	}
}

func retryable(err error) bool {
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return false
	}
	var resp minio.ErrorResponse
	if errors.As(err, &resp) {
		switch resp.StatusCode {
		case 408, 429, 500, 502, 503, 504:
			return true
		}
		return false
	}
	// network-level errors: resets, timeouts, unexpected EOF, ...
	return true
}

func (r *retryReader) Close() error {
	if r.rc == nil {
		return nil
	}
	return r.rc.Close()
}
