carbonzipper: carbonserver proxy for graphite-web
=================================================

CarbonZipper is the central part of a replacement graphite storage stack.  It
proxies requests from graphite-web to a cluster of carbon storage backends.
Previous versions (available in the git history) were able to talk to python
carbon stores, but the current version requires the use of
[carbonserver](https://github.com/grobian/carbonserver).

Configuration is done via a JSON file loaded at startup.  The only required
field is the list of carbonserver backends to connect to.

Other pieces of the stack are:
   - [carbonapi](https://github.com/dgryski/carbonapi)
   - [carbonmem](https://github.com/dgryski/carbonmem)

Acknowledgement
---------------
This program was originally developed for Booking.com.  With approval
from Booking.com, the code was generalised and published as Open Source
on github, for which the author would like to express his gratitude.

License
-------

This code is licensed under the MIT license.
