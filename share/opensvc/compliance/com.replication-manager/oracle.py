#!/usr/bin/env python

import os
import sys
import tempfile
from subprocess import *
import json

sys.path.append(os.path.dirname(__file__))

import files
import fs
from comp import *
from utilities import which

class Oracle(object):
    def __init__(self, prefix='OSVC_COMP_ORACLESERVER_FORM_DB_'):
        self.env_bkp = os.environ.copy()
        self.prefix = prefix.upper()
        self.data = []
        self.dir = os.path.dirname(__file__)
        self.oradir = os.path.join(self.dir, '..', 'fr.sncf.oracle')
        self.orainst = os.path.join(self.oradir, 'oracle_software_install.ksh')

        for k in [ key for key in os.environ if key.startswith(self.prefix)]:
            try:
                d = json.loads(os.environ[k])
                self.data.append(d)
            except ValueError:
                print >>sys.stderr, 'failed to parse variable', os.environ[k]
            except Exception, e:
                print >>sys.stderr, \
                  'unknown error parsing variable', os.environ[k], \
                  ": ", str(e)

        if len(self.data) == 0:
            print "no applicable variable found in rulesets", self.prefix
            raise NotApplicable()

    def mounted(self):
        key = "OSVC_COMP_SVCMON_MON_AVAILSTATUS"
        availstatus = os.environ.get(key)
        if availstatus is None:
            return True
        elif availstatus in ("n/a", "stdby up", "up"):
            return True
        return False

    def fixable(self):
        return RET_OK

    def check_inst(self, inst, verbose=False):
        r = 0
        if self.mounted():
            r += files.CompFiles('OSVC_COMP_ORACLESERVER_FORM_DB_FILE').check()
            r += fs.CompFs('OSVC_COMP_ORACLESERVER_FORM_DB_FS').check()
        if r > 0:
            if verbose: print >>sys.stderr, ""
            return 1
        if verbose: print ""
        return 0

    def fix_inst(self, inst):
        return 0

    def setup_env(self, inst):
        for key, val in inst.items():
            os.environ[key.upper()] = val

    def check(self):
        r = 0
        for inst in self.data:
            self.setup_env(inst)
            r |= self.check_inst(inst, verbose=True)
            os.environ.clear()
            os.environ.update(self.env_bkp)
        return r

    def fix(self):
        try:
            self.get_ogm()
        except:
            return 1
        r = 0
        for inst in self.data:
            self.setup_env(inst)
            if self.check_inst(inst, verbose=False) == RET_ERR:
                r += self.fix_inst(inst)
            os.environ.clear()
            os.environ.update(self.env_bkp)
        return r

if __name__ == "__main__":
    syntax = """syntax:
      %s PREFIX check|fixable|fix"""%sys.argv[0]
    if len(sys.argv) != 3:
        print >>sys.stderr, "wrong number of arguments"
        print >>sys.stderr, syntax
        sys.exit(RET_ERR)
    try:
        o = Oracle(sys.argv[1])
        if sys.argv[2] == 'check':
            RET = o.check()
        elif sys.argv[2] == 'fix':
            RET = o.fix()
        elif sys.argv[2] == 'fixable':
            RET = o.fixable()
        else:
            print >>sys.stderr, "unsupported argument '%s'"%sys.argv[2]
            print >>sys.stderr, syntax
            RET = RET_ERR
    except NotApplicable:
        sys.exit(RET_NA)
    except Exception, e:
        print e
        sys.exit(RET_ERR)

    sys.exit(RET)

