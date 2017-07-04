#!/usr/bin/env python
""" 
[{"service": "foo", "level": "2345", "state": "on"},
 {"service": "foo", "level": "016", "state": "off"},
 {"service": "bar", "state": "on"},
 ...]
"""

import os
import sys
import json
import pwd
import re
from subprocess import *

sys.path.append(os.path.dirname(__file__))

from comp import *

class CompRc(object):
    def __init__(self, prefix='OSVC_COMP_RC_'):
        self.prefix = prefix.upper()
        self.sysname, self.nodename, x, x, self.machine = os.uname()

        self.services = []
        for k in [key for key in os.environ if key.startswith(self.prefix)]:
            try:
                l = json.loads(os.environ[k])
                for i, d in enumerate(l):
                    for key, val in d.items():
                        d[key] = self.subst(val)
                    l[i] = d
                self.services += l
            except ValueError:
                perror('failed to concatenate', os.environ[k], 'to service list')

        self.validate_svcs()

        if len(self.services) == 0:
            raise NotApplicable()

        if self.sysname not in ['Linux', 'HP-UX']:
            perror(__file__, 'module not supported on', self.sysname)
            raise NotApplicable()

        vendor = os.environ.get('OSVC_COMP_NODES_OS_VENDOR', 'unknown')
        release = os.environ.get('OSVC_COMP_NODES_OS_RELEASE', 'unknown')
        if vendor in ['CentOS', 'Redhat', 'Red Hat', 'SuSE'] or \
           (vendor == 'Oracle' and self.sysname == 'Linux'):

            import chkconfig
            self.o = chkconfig.Chkconfig()
        elif vendor in ['Ubuntu', 'Debian', 'HP']:
            import sysvinit
            self.o = sysvinit.SysVInit()
        else:
            perror(vendor, "not supported")
            raise NotApplicable()

    def subst(self, v):
        if type(v) == list:
            l = []
            for _v in v:
                l.append(self.subst(_v))
            return l
        if type(v) != str and type(v) != unicode:
            return v
        p = re.compile('%%ENV:\w+%%')
        for m in p.findall(v):
            s = m.strip("%").replace('ENV:', '')
            if s in os.environ:
                _v = os.environ[s]
            elif 'OSVC_COMP_'+s in os.environ:
                _v = os.environ['OSVC_COMP_'+s]
            else:
                perror(s, 'is not an env variable')
                raise NotApplicable()
            v = v.replace(m, _v)
        return v

    def validate_svcs(self):
        l = []
        for i, svc in enumerate(self.services):
            if self.validate_svc(svc) == RET_OK:
                l.append(svc)
        self.svcs = l

    def validate_svc(self, svc):
        if 'service' not in svc:
            perror(svc, ' rule is malformed ... service key not present')
            return RET_ERR
        if 'state' not in svc:
            perror(svc, ' rule is malformed ... state key not present')
            return RET_ERR
        return RET_OK

    def check_svc(self, svc, verbose=True):
        if 'seq' in svc:
            seq = svc['seq']
        else:
            seq = None
        return self.o.check_state(svc['service'], svc['level'], svc['state'], seq=seq, verbose=verbose)

    def fix_svc(self, svc, verbose=True):
        if 'seq' in svc:
            seq = svc['seq']
        else:
            seq = None
        if self.check_svc(svc, verbose=False) == RET_OK:
            return RET_OK
        return self.o.fix_state(svc['service'], svc['level'], svc['state'], seq=seq)

    def check(self):
        r = 0
        for svc in self.services:
            r |= self.check_svc(svc)
        return r

    def fix(self):
        r = 0
        for svc in self.services:
            r |= self.fix_svc(svc)
        return r

if __name__ == "__main__":
    syntax = """syntax:
      %s PREFIX check|fixable|fix"""%sys.argv[0]
    if len(sys.argv) != 3:
        perror("wrong number of arguments")
        perror(syntax)
        sys.exit(RET_ERR)
    try:
        o = CompRc(sys.argv[1])
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

