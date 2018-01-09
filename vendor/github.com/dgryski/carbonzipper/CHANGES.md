Changes
=================================================

[Fix] - bugfix

**[Breaking]** - breaking change

[Feature] - new feature

[Improvement] - non-breaking improvement

[Code] - code quality related change that shouldn't make any significant difference for end-user


Changes
-------
**0.73.2**
   - [Code] Remove carbonapi cross-dependency by moving context handling code to util

**0.73**
   - **[Breaking]** [protobuf go-api] Protobuf protocol itself haven't changed, this only affects people who import carbonzipperpb package directly from this repo. Library now declares most of its structures as non-nullable. This changes API of carbonzipperpb and pb3, now they contain less pointers. That should reduce GC pressure a little bit. Sholdn't affect people who use it over the network.
   - [Improvement] Make connect timeout configurable.
   - [Improvement] Make keep alive interval configurable

**0.72**
   - [Fix] Fix /info handler (bug was introduced after splitting zipper into several packages)

**0.71**
   - [Fix] carbonsearch was not properly configured (bug introduced after splitting zipper into several packages)

**0.70**
   - **[Breaking]** Logging migrated to zap (structured logging). Log format changed significantly. Old command line options removed. Please consult example.conf for a new config options and explanations
   - **[Breaking]** Change config format from json to yaml. Also we've changed config structure and command line options. Please refer to example.conf for decent example of new format
   - [Improvement] Add context support. Also log context from carbonapi
   - [Improvement] Use dep as a vendoring tool
   - [Improvement] Add a Makefile that will hide some magic from user
   - [Improvement] graphite-web 1.0 support
   - [Fix] Fix incompatibility between carbonzipper and older versions of carbonserver/go-carbon (protobuf2-only)
   - [Code] Split carbonzipper into several packages

Notes on upgrading:

Even though there are several changes that's marked as breaking, it only breaks local config parsing and changes logging format. Please note that on high-load environments access log can be huge.

**0.63**
   - [Fix] carbonsearch query cache was never cleared

**0.62**
   - [Fix] Fix carbonsearch queries with recent carbonapi version
   - [Fix] Fix pathCache to handle render requests with globs.
   - [Feature] Add cache for carbonsearch results

**0.61**
   - [Fix] Fix rewrite for internal queries, because of an error some queries were sent as protobuf not as protobuf3
   - [Code] gofmt the code!

**0.60**
   - **[Breaking]** Carbonzipper backend protocol changed to protobuf3. Though output for /render, /info /find can be both (format=protobuf3 for protobuf3, format=protobuf for protobuf2).

**0.50**
   - See commit log.
