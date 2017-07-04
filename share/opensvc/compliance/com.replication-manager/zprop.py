#!/usr/bin/env python

import os
import sys

sys.path.append(os.path.dirname(__file__))

from utilities import which
from comp import *
from subprocess import *

class CompZprop(CompObject):
    def __init__(self, prefix='OSVC_COMP_ZPROP_'):
        CompObject.__init__(self, prefix=prefix, data=data)

    def init(self):
        self.sysname, self.nodename, x, x, self.machine = os.uname()
        self.data = []

        for rule in self.get_rules():
            try:
                self.data += self.add_rule(rule)
            except InitError:
                continue
            except ValueError:
                perror('failed to parse variable', rule)

    def add_rule(self, d):
        allgood = True
        for k in ["name", "prop", "op", "value"]:
            if k not in d:
                perror('the', k, 'key should be in the dict:', d)
                allgood = False
        if allgood:
            return [d]
        return []

    def get_prop(self, d):
        cmd = [self.zbin, "get", d.get("prop"), d.get("name")]
        p = Popen(cmd, stdout=PIPE, stderr=PIPE)
        out, err = p.communicate()
        if p.returncode != 0:
            return
        out = bdecode(out)
        l = [line for line in out.splitlines() if line != ""]
        if len(l) != 2:
            return
        v1 = l[0].split()
        v2 = l[1].split()
        if len(v1) != len(v2):
            return
        data = {}
        for k, v in zip(v1, v2):
            data[k] = v
        return data

    def check_le(self, current, target):
        current = int(current)
        if current <= target:
            return RET_OK
        return RET_ERR

    def check_ge(self, current, target):
        current = int(current)
        if current >= target:
            return RET_OK
        return RET_ERR

    def check_lt(self, current, target):
        current = int(current)
        if current < target:
            return RET_OK
        return RET_ERR

    def check_gt(self, current, target):
        current = int(current)
        if current > target:
            return RET_OK
        return RET_ERR

    def check_eq(self, current, target):
        if current == str(target):
            return RET_OK
        return RET_ERR

    def fixable(self):
        return RET_NA

    def fix_zprop(self, d):
        if self.check_zprop(d) == RET_OK:
            return RET_OK
        prop = d.get("prop")
        target = d.get("value")
        name = d.get("name")
        cmd = [self.zbin, "set", prop+"="+target, name]
        pinfo(" ".join(cmd))
        p = Popen(cmd, stdout=PIPE, stderr=PIPE)
        out, err = p.communicate()
        if p.returncode != 0:
            err = bdecode(err)
            if len(err) > 0:
                perror(err)
            return RET_ERR
        return RET_OK

    def check_zprop(self, d, verbose=False):
        v = self.get_prop(d)
        prop = d.get("prop")
        if v is None:
            if verbose:
                perror("property", prop, "does not exist")
            return RET_ERR
        current = v["VALUE"]
        op = d.get("op")
        target = d.get("value")
        if op == "=":
            r = self.check_eq(current, target)
        elif op == "<=":
            r = self.check_le(current, target)
        elif op == "<":
            r = self.check_lt(current, target)
        elif op == ">=":
            r = self.check_ge(current, target)
        elif op == ">":
            r = self.check_gt(current, target)
        else:
            perror("unsupported operator", op)
            return RET_ERR
        if verbose:
            if r == RET_OK:
                pinfo("property %s current value %s is %s %s. on target." % (prop, current, op, target))
            else:
                pinfo("property %s current value %s is not %s %s." % (prop, current, op, target))
        return r

    def check_zbin(self):
        return which(self.zbin)

    def check(self):
        if not self.check_zbin():
            pinfo(self.zbin, "not found")
            return RET_NA
        r = 0
        for d in self.data:
            r |= self.check_zprop(d, verbose=True)
        return r

    def fix(self):
        if not self.check_zbin():
            pinfo(self.zbin, "not found")
            return RET_NA
        r = 0
        for d in self.data:
            r |= self.fix_zprop(d)
        return r

if __name__ == "__main__":
    main(CompZprop)

