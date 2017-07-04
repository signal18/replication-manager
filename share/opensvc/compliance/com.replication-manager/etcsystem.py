#!/usr/bin/env python

data = {
  "default_prefix": "OSVC_COMP_ETCSYSTEM_",
  "example_value": """ [{"key": "fcp:fcp_offline_delay", "op": ">=", "value": 21}, {"key": "ssd:ssd_io_time", "op": "=", "value": "0x3C"}] """,
  "description": "Checks and setup values in /etc/system respecting strict targets or thresholds.",
  "form_definition": """
Desc: |
  A rule to set a list of Solaris kernel parameters to be set in /etc/system. Current values can be checked as strictly equal, or superior/inferior to their target value.
Css: comp48

Outputs:
  -
    Dest: compliance variable
    Type: json
    Format: list of dict
    Class: etcsystem

Inputs:
  -
    Id: key
    Label: Key
    DisplayModeLabel: key
    LabelCss: action16
    Mandatory: Yes
    Type: string
    Help: The /etc/system parameter to check.

  -
    Id: value
    Label: Value
    DisplayModeLabel: value
    LabelCss: action16
    Mandatory: Yes
    Type: string or integer
    Help: The /etc/system parameter target value.

  -
    Id: op
    Label: Comparison operator
    DisplayModeLabel: op
    LabelCss: action16
    Mandatory: Yes
    Type: string
    Default: "="
    Candidates:
      - "="
      - ">"
      - ">="
      - "<"
      - "<="
    Help: The comparison operator to use to check the parameter current value.
""",
}

import os
import sys
from subprocess import *

sys.path.append(os.path.dirname(__file__))

from comp import *

class EtcSystem(CompObject):
    def __init__(self, prefix=None):
        CompObject.__init__(self, prefix=prefix, data=data)

    def init(self):
        self.keys = self.get_rules()
        if len(self.keys) == 0:
            raise NotApplicable()

        self.data = {}
        self.cf = os.path.join(os.sep, 'etc', 'system')
        self.load_file(self.cf)

    def fixable(self):
        return RET_OK

    def load_file(self, p):
        if not os.path.exists(p):
            perror(p, "does not exist")
            return
        with open(p, 'r') as f:
            buff = f.read()
        self.lines = buff.split('\n')
        for i, line in enumerate(self.lines):
            line = line.strip()
            if line.startswith('*'):
                continue
            if len(line) == 0:
                continue
            l = line.split()
            if l[0] != "set":
                continue
            if len(l) < 2:
                continue
            line = ' '.join(l[1:]).split('*')[0]
            var, val = line.split('=')
            var = var.strip()
            val = val.strip()
            try:
                val = int(val)
            except:
                pass
            if var in self.data:
                self.data[var].append([val, i])
            else:
                self.data[var] = [[val, i]]

    def set_val(self, keyname, target, op):
        newline = 'set %s = %s'%(keyname, str(target))
        if keyname not in self.data:
            pinfo("add '%s' to /etc/system"%newline)
            self.lines.insert(-1, newline + " * added by opensvc")
        else:
            ok = 0
            for value, ref in self.data[keyname]:
                r = self._check_key(keyname, target, op, value, ref, verbose=False)
                if r == RET_ERR:
                    pinfo("comment out line %d: %s"%(ref, self.lines[ref]))
                    self.lines[ref] = '* '+self.lines[ref]+' * commented out by opensvc'
                else:
                    ok += 1
            if ok == 0:
                pinfo("add '%s' to /etc/system"%newline)
                self.lines.insert(-1, newline + " * added by opensvc")

    def get_val(self, keyname):
        if keyname not in self.data:
            return []
        return self.data[keyname]

    def _check_key(self, keyname, target, op, value, ref, verbose=True):
        r = RET_OK
        if value is None:
            if verbose:
                perror("%s not set"%keyname)
            r |= RET_ERR
        if op == '=':
            if str(value) != str(target):
                if verbose:
                    perror("%s=%s, target: %s"%(keyname, str(value), str(target)))
                r |= RET_ERR
            elif verbose:
                pinfo("%s=%s on target"%(keyname, str(value)))
        else:
            if type(value) != int:
                if verbose:
                    perror("%s=%s value must be integer"%(keyname, str(value)))
                r |= RET_ERR
            elif op == '<=' and value > target:
                if verbose:
                    perror("%s=%s target: <= %s"%(keyname, str(value), str(target)))
                r |= RET_ERR
            elif op == '>=' and value < target:
                if verbose:
                    perror("%s=%s target: >= %s"%(keyname, str(value), str(target)))
                r |= RET_ERR
            elif verbose:
                pinfo("%s=%s on target"%(keyname, str(value)))
        return r

    def check_key(self, key, verbose=True):
        if 'key' not in key:
            if verbose:
                perror("'key' not set in rule %s"%str(key))
            return RET_NA
        if 'value' not in key:
            if verbose:
                perror("'value' not set in rule %s"%str(key))
            return RET_NA
        if 'op' not in key:
            op = "="
        else:
            op = key['op']
        target = key['value']

        if op not in ('>=', '<=', '='):
            if verbose:
                perror("'value' list member 0 must be either '=', '>=' or '<=': %s"%str(key))
            return RET_NA

        keyname = key['key']
        data = self.get_val(keyname)

        if len(data) == 0:
            perror("%s key is not set"%keyname)
            return RET_ERR

        r = RET_OK
        ok = 0
        for value, ref in data:
            r |= self._check_key(keyname, target, op, value, ref, verbose)
            if r == RET_OK:
                ok += 1

        if ok > 1:
            perror("duplicate lines for key %s"%keyname)
            r |= RET_ERR
        return r

    def fix_key(self, key):
        self.set_val(key['key'], key['value'], key['op'])

    def check(self):
        r = 0
        for key in self.keys:
            r |= self.check_key(key, verbose=True)
        return r

    def fix(self):
        for key in self.keys:
            if self.check_key(key, verbose=False) == RET_ERR:
                self.fix_key(key)
        if len(self.keys) > 0:
            import datetime
            backup = self.cf+str(datetime.datetime.now())
            try:
                import shutil
                shutil.copy(self.cf, backup)
            except:
                perror("failed to backup %s"%self.cf)
                return RET_ERR
            try:
                with open(self.cf, 'w') as f:
                    f.write('\n'.join(self.lines))
            except:
                perror("failed to write %s"%self.cf)
                return RET_ERR
        return RET_OK

if __name__ == "__main__":
    main(EtcSystem)
