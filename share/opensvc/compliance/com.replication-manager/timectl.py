#!/usr/bin/env python

import os
import sys
import time

sys.path.append(os.path.dirname(__file__))

from comp import *

class CompTimectl(object):
    def __init__(self, val=3):
        self.val = str(val)

    def fix(self):
        sys.stdout.write('Sleeping '+self.val+' seconds ')
        sys.stdout.flush()
        t = int(self.val)
        while t > 0:
            time.sleep(1)
            t -= 1
            if t%5 == 0:
                sys.stdout.write(str(t))
            else:
                sys.stdout.write('.')
            sys.stdout.flush()
        pinfo('')
        return RET_OK

    def fixable(self):
        return RET_NA

    def check(self):
        return RET_OK

if __name__ == "__main__":
    syntax = """syntax:
      %s value check|fixable|fix"""%sys.argv[0]
    if len(sys.argv) != 3:
        perror("wrong number of arguments")
        perror(syntax)
        sys.exit(RET_ERR)
    try:
        o = CompTimectl(sys.argv[1])
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
    except NotApplicable:
        sys.exit(RET_NA)
    except:
        import traceback
        traceback.print_exc()
        sys.exit(RET_ERR)

    sys.exit(RET)

