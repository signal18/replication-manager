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

/*
Package cairo wraps the Cairo graphics library.

http://cairographics.org/

Most functions are one-to-one with the corresponding Cairo functions.
See the C documentation at http://cairographics.org/manual/ for
details.

Subtyping

Though Cairo is a C API, it has a simple notion of subtypes; for
example, an ImageSurface is a Surface with some extra methods.

These are implemented in the Go API as two separate types with
embedding.  Any method on a Surface can also be called on an
ImageSurface (and similarly for Pattern and MeshPattern and so on).
If a function expects a Surface as an argument and you have an
ImageSurface, you must call it like foo(imageSurface.Surface).

Error Handling

Cairo's C API handles errors in a way similar to C's errno -- you're
supposed to check for an error after making each Cairo call.  But in
practice the only errors Cairo can encounter are programmer errors,
such as calling PopGroup() when you haven't called PushGroup() first.

Because of this, this library implicitly checks the error value after
every call and panic()s on any error.  The value passed to panic() is
a cairo.Status which can be compared against various constants; it
also implements Error so it can be stringified.

(There's a few places that still accidentally return an error, but
those will be fixed.)
*/
package cairo
