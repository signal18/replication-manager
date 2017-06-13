#!/usr/bin/env python

data = {
  "default_prefix": "OSVC_COMP_BIOS_",
  "example_value": "0.6.0",
  "description": """* Checks an exact BIOS version, as returned by dmidecode or sysfs
* Module need to be called with the exposed bios version as variable (bios.py $OSVC_COMP_TEST_BIOS_1 check)
""",
}

import os
import sys
from subprocess import *

sys.path.append(os.path.dirname(__file__))

from comp import *

class CompBios(CompObject):
    def __init__(self, prefix=None):
        CompObject.__init__(self, prefix=prefix, data=data)

    def init(self):
        self.rules = self.get_rules_raw()
        self.sysname, self.nodename, x, x, self.machine = os.uname()
        if self.sysname not in ['Linux']:
            perror('module not supported on', self.sysname)
            raise NotApplicable()

    def get_bios_version_Linux(self):
        p = os.path.join(os.sep, 'sys', 'class', 'dmi', 'id', 'bios_version')
        try:
            f = open(p, 'r')
            ver = f.read().strip()
            f.close()
            return ver
        except:
            pass

        try:
            cmd = ['dmidecode', '-t', 'bios']
            p = Popen(cmd, stdout=PIPE)
            out, err = p.communicate()
            if p.returncode != 0:
                raise
            out = bdecode(out)
            for line in out.splitlines():
                if 'Version:' in line:
                    return line.split(':')[-1].strip()
            raise
        except:
            perror('can not fetch bios version')
            return None
        return ver

    def fixable(self):
        return RET_NA

    def check(self):
        self.ver = self.get_bios_version_Linux()
        if self.ver is None:
            return RET_NA
        r = RET_OK
        for rule in self.rules:
            r |= self._check(rule)
        return r

    def _check(self, rule):
        if self.ver == rule:
            pinfo("bios version is %s, on target" % self.ver)
            return RET_OK
        perror("bios version is %s, target %s" % (self.ver, rule))
        return RET_ERR

    def fix(self):
        return RET_NA

if __name__ == "__main__":
    main(CompBios)
