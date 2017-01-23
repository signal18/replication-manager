// Copyright 2015 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package scale provides abstractions for scales that map from one
// domain to another and provide methods for indicating human-readable
// intervals in the input domain. The most common type of scale is a
// quantitative scale, such as a linear or log scale, which is
// captured by the Quantitative interface.
package scale
