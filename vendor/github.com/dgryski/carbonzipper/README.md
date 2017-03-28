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
   - [carbonsearch](https://github.com/kanatohodets/carbonsearch)


Changes and versioning
----------------------

Version policy - all the versions we run in production is taged.

In case change will require simultanious upgrade of different components, it will be stated in Upgrading notes below.

Also we will try to maintain backward compatibility from down to up.

For example with protobuf2 -> protobuf3 migration - carbonzipper can still send results to older versions of carbonapi, but can't get results from older versions of carbonserver/go-carbon.

See [CHANGES.md](https://github.com/dgryski/carbonzipper/blob/master/CHANGES.md)

Upgrading to 0.60 from 0.50 or earlier
--------------------------------------

Starting from 0.60, carbonzipper will be able to talk **only** with storages compatible with **protobuf3**.

At this moment (0.60) it's only go-carbon, starting from commit ee2bc24 (post 0.9.1)

Carbonzipper can still return results in protobuf and compatibility won't be removed at least until Summer 2017.

If you want to upgrade, the best option is to do follwing steps:

1. Migrate to go-carbon post 0.9.1 release. (note: carbonserver isn't compatible with this version of zipper)
2. Migrate to carbonsearch 0.16.0 (if you are using any)
3. Upgrade carbonzipper to 0.60 or newer.
4. Upgrade carbonapi to 0.6.0 (commit 119e346 or newer) (optional, but advised)


Acknowledgement
---------------
This program was originally developed for Booking.com.  With approval
from Booking.com, the code was generalised and published as Open Source
on github, for which the author would like to express his gratitude.

License
-------

This code is licensed under the MIT license.


Contact
-------

If you have questions or problems there are two ways to contact us:

1. Open issue on a github page
2. #zipperstack on [gophers slack](https://invite.slack.golangbridge.org/)
