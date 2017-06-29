#!/usr/bin/env python
""" 
Same as files compliance object, but verifies the sudoers
declaration syntax using visudo in check mode.

The variable format is json-serialized:

{
  "path": "/some/path/to/file",
  "fmt": "root@corp.com		%%HOSTNAME%%@corp.com",
  "uid": 500,
  "gid": 500,
}

Wildcards:
%%ENV:VARNAME%%		Any environment variable value
%%HOSTNAME%%		Hostname
%%SHORT_HOSTNAME%%	Short hostname

"""

import os
import sys
from subprocess import *

sys.path.append(os.path.dirname(__file__))

from comp import *
from files import CompFiles

class CompSudoers(CompFiles):
    def check_file_syntax(self, f, verbose=False):
        cmd = ['visudo', '-c', '-f', '-']
        p = Popen(cmd, stdin=PIPE, stdout=PIPE, stderr=PIPE)
        out, err = p.communicate(input=bencode(f['fmt']))
        if p.returncode != 0:
            if verbose:
                perror("target sudoers rules syntax error.")
            else:
                perror("target sudoers rules syntax error. abort installation.")
        return p.returncode

    def check(self):
        r = 0
        for f in self.files:
            r |= self.check_file_syntax(f, verbose=True)
            r |= self.check_file(f, verbose=True)
        return r

    def fix(self):
        r = 0
        for f in self.files:
            if self.check_file_syntax(f):
                r |= 1
                # refuse to install a corrupted sudoers file
                continue
            r |= self.fix_file_fmt(f)
            r |= self.fix_file_mode(f)
            r |= self.fix_file_owner(f)
        return r


if __name__ == "__main__":
    syntax = """syntax:
      %s PREFIX check|fixable|fix"""%sys.argv[0]
    if len(sys.argv) != 3:
        perror("wrong number of arguments")
        perror(syntax)
        sys.exit(RET_ERR)
    try:
        o = CompSudoers(sys.argv[1])
        if sys.argv[2] == 'check':
            RET = o.check()
        elif sys.argv[2] == 'fix':
            RET = o.fix()
        elif sys.argv[2] == 'fixable':
            RET = o.fixable()
        else:
            perror("unsupported argument '%s'"%sys.argv[2])
            perror(syntax)
            RET = RET_ERR
    except ComplianceError:
        sys.exit(RET_ERR)
    except NotApplicable:
        sys.exit(RET_NA)
    except:
        import traceback
        traceback.print_exc()
        sys.exit(RET_ERR)

    sys.exit(RET)

