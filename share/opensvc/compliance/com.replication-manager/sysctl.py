#!/usr/bin/env python

data = {
  "default_prefix": "OSVC_COMP_SYSCTL_",
  "example_value": """ 
{
  "key": "vm.lowmem_reserve_ratio",
  "index": 1,
  "op": ">",
  "value": 256
}
  """,
  "description": """* Verify a linux kernel parameter value is on target
* Live parameter value (sysctl executable)
* Persistent parameter value (/etc/sysctl.conf)
""",
  "form_definition": """
Desc: |
  A rule to set a list of Linux kernel parameters to be set in /etc/sysctl.conf. Current values can be checked as strictly equal, or superior/inferior to their target value. Each field in a vectored value can be tuned independantly using the index key.
Css: comp48

Outputs:
  -
    Dest: compliance variable
    Type: json
    Format: list of dict
    Class: sysctl

Inputs:
  -
    Id: key
    Label: Key
    DisplayModeLabel: key
    LabelCss: action16
    Mandatory: Yes
    Type: string
    Help: The /etc/sysctl.conf parameter to check.

  -
    Id: index
    Label: Index
    DisplayModeLabel: idx
    LabelCss: action16
    Mandatory: Yes
    Default: 0
    Type: integer
    Help: The /etc/sysctl.conf parameter to check.

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

  -
    Id: value
    Label: Value
    DisplayModeLabel: value
    LabelCss: action16
    Mandatory: Yes
    Type: string or integer
    Help: The /etc/sysctl.conf parameter target value.
""",
}

import os
import sys
import json
import pwd
from subprocess import *

sys.path.append(os.path.dirname(__file__))

from comp import *

class Sysctl(CompObject):
    def __init__(self, prefix=None):
        CompObject.__init__(self, prefix=prefix, data=data)

    def init(self):
        if os.uname()[0] != "Linux":
            raise NotApplicable()
        self.need_reload = False
        self.cf = os.path.join(os.sep, "etc", "sysctl.conf")
        if not os.path.exists(self.cf):
            perror(self.cf, 'does not exist')
            raise NotApplicable()

        self.keys = []
        self.cache = None

        self.keys = self.get_rules()

        if len(self.keys) == 0:
            raise NotApplicable()

        self.convert_keys()


    def fixable(self):
        return RET_OK

    def parse_val(self, val):
        val = list(map(lambda x: x.strip(), val.strip().split()))
        for i, e in enumerate(val):
            try:
                val[i] = int(e)
            except:
                pass
        return val

    def get_keys(self):
        with open(self.cf, 'r') as f:
            buff = f.read()

        if self.cache is None:
            self.cache = {}

        for line in buff.splitlines():
            line = line.strip()
            if line.startswith('#'):
                continue
            l = line.split('=')
            if len(l) != 2:
                continue
            key = l[0].strip()
            val = self.parse_val(l[1])
            self.cache[key] = val

    def get_live_key(self, key):
        p = Popen(['sysctl', key], stdout=PIPE, stderr=PIPE)
        out, err = p.communicate()
        if p.returncode != 0:
            return None
        l = bdecode(out).split('=')
        if len(l) != 2:
            return None
        val = self.parse_val(l[1])
        return val
 
    def get_key(self, key):
        if self.cache is None:
            self.get_keys()
        if key not in self.cache:
            return None
        return self.cache[key]

    def fix_key(self, key):
        done = False
        target = key['value']
        index = key['index']

        with open(self.cf, 'r') as f:
            buff = f.read()

        lines = buff.split('\n')
        for i, line in enumerate(lines):
            line = line.strip()
            if line.startswith('#'):
                continue
            l = line.split('=')
            if len(l) != 2:
                continue
            keyname = l[0].strip()
            if key['key'] != keyname:
                continue
            if done:
                pinfo("sysctl: remove redundant key %s"%keyname)
                del lines[i]
                continue
            val = self.parse_val(l[1])
            if target == val[index]:
                done = True
                continue
            pinfo("sysctl: set %s[%d] = %s"%(keyname, index, str(target)))
            val[index] = target
            lines[i] = "%s = %s"%(keyname, " ".join(map(str, val)))
            done = True

        if not done:
            # if key is not in sysctl.conf, get the value from kernel
            val = self.get_live_key(key['key'])
            if val is None:
                perror("key '%s' not found in live kernel parameters" % key['key'])
                return RET_ERR
            if target != val[index]:
                val[index] = target
            pinfo("sysctl: set %s = %s"%(key['key'], " ".join(map(str, val))))
            lines += ["%s = %s"%(key['key'], " ".join(map(str, val)))]

        try:
            with open(self.cf, 'w') as f:
                f.write('\n'.join(lines))
        except:
            perror("failed to write sysctl.conf")
            return RET_ERR

        return RET_OK

    def convert_keys(self):
        keys = []
        for key in self.keys:
            keyname = key['key']
            value = key['value']
            if type(value) == list:
                if len(value) > 0 and type(value[0]) != list:
                    value = [value]
                for i, v in enumerate(value):
                    keys.append({
                      "key": keyname,
                      "index": i,
                      "op": v[0],
                      "value": v[1],
                    })
            elif 'key' in key and 'index' in key and 'op' in key and 'value' in key:
               keys.append(key)

        self.keys = keys

    def check_key(self, key, verbose=False):
        r = RET_OK
        keyname = key['key']
        target = key['value']
        op = key['op']
        i = key['index']
        current_value = self.get_key(keyname)
        current_live_value = self.get_live_key(keyname)

        if current_value is None:
            if verbose:
                perror("key '%s' not found in sysctl.conf"%keyname)
            return RET_ERR

        if op == "=" and str(current_value[i]) != str(target):
            if verbose:
                perror("sysctl err: %s[%d] = %s, target: %s"%(keyname, i, str(current_value[i]), str(target)))
            r |= RET_ERR
        elif op == ">=" and type(target) == int and current_value[i] < target:
            if verbose:
                perror("sysctl err: %s[%d] = %s, target: >= %s"%(keyname, i, str(current_value[i]), str(target)))
            r |= RET_ERR
        elif op == "<=" and type(target) == int and current_value[i] > target:
            if verbose:
                perror("sysctl err: %s[%d] = %s, target: <= %s"%(keyname, i, str(current_value[i]), str(target)))
            r |= RET_ERR
        else:
            if verbose:
                pinfo("sysctl ok: %s[%d] = %s, on target"%(keyname, i, str(current_value[i])))

        if r == RET_OK and current_live_value is not None and current_value != current_live_value:
            if verbose:
                perror("sysctl err: %s on target in sysctl.conf but kernel value is different"%(keyname))
            self.need_reload = True
            r |= RET_ERR

        return r

    def check(self):
        r = 0
        for key in self.keys:
            r |= self.check_key(key, verbose=True)
        return r

    def reload_sysctl(self):
        cmd = ['sysctl', '-e', '-p']
        pinfo("sysctl:", " ".join(cmd))
        p = Popen(cmd, stdout=PIPE, stderr=PIPE)
        p.communicate()
        if p.returncode != 0:
            perror("reload failed")
            return RET_ERR
        return RET_OK

    def fix(self):
        r = 0
        for key in self.keys:
            if self.check_key(key, verbose=False) == RET_ERR:
                self.need_reload = True
                r |= self.fix_key(key)
        if self.need_reload:
            r |= self.reload_sysctl()
        return r

if __name__ == "__main__":
    main(Sysctl)
