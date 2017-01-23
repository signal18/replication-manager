// Copyright 2015 Google Inc. All Rights Reserved.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package cairo

import "unsafe"

// PathSegments are produced by iterating paths.
type PathSegment struct {
	Type   PathDataType
	Points []PathPoint
}

// PathPoints are produced by iterating paths.
type PathPoint struct {
	X, Y float64
}

// Matches cairo_path_data_t.header.
type pathDataHeader struct {
	dataType int32
	length   int32
}

// decodePathSegment extracts a series of points out of a cairo_path_data_t array.
func decodePathSegment(pathData unsafe.Pointer) (*PathSegment, int) {
	header := (*pathDataHeader)(pathData)
	seg := PathSegment{
		Type:   PathDataType(header.dataType),
		Points: make([]PathPoint, header.length-1),
	}
	parts := (*[1 << 30]PathPoint)(pathData)
	copy(seg.Points, parts[1:])
	return &seg, int(header.length)
}
