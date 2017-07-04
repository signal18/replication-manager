#!/usr/bin/env python

data = {
  "default_prefix": "OSVC_COMP_NODECONF_",
  "example_value": """
[
  {
    "key": "node.repopkg",
    "op": "=",
    "value": "ftp://ftp.opensvc.com/opensvc"
  },
  {
    "key": "node.repocomp",
    "op": "=",
    "value": "ftp://ftp.opensvc.com/compliance"
  }
]
""",
  "description": """* Verify opensvc agent configuration parameter
""",
  "form_definition": """
Desc: |
  A rule to set a parameter in OpenSVC node.conf configuration file. Used by the 'nodeconf' compliance object.
Css: comp48
Outputs:
  -
    Dest: compliance variable
    Type: json
    Format: list of dict
    Class: nodeconf
Inputs:
  -
    Id: key
    Label: Key
    DisplayModeLabel: key
    LabelCss: action16
    Mandatory: Yes
    Type: string
    Help: The OpenSVC node.conf parameter to check.
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
    Help: The comparison operator to use to check the parameter value.
  -
    Id: value
    Label: Value
    DisplayModeLabel: value
    LabelCss: action16
    Mandatory: Yes
    Type: string or integer
    Help: The OpenSVC node.conf parameter value to check.
""",
}


import os
import sys
import json
import re
from subprocess import *

sys.path.append(os.path.dirname(__file__))

from comp import *

class NodeConf(CompObject):
    def __init__(self, prefix=None):
        CompObject.__init__(self, prefix=prefix, data=data)

    def init(self):
        self.keys = self.get_rules()

    def fixable(self):
        return RET_OK

    def unset_val(self, keyname):
        cmd = ['nodemgr', 'unset', '--param', keyname]
        pinfo(' '.join(cmd))
        p = Popen(cmd, stdout=PIPE, stderr=PIPE)
        out, err = p.communicate()
        return p.returncode

    def set_val(self, keyname, target):
        if type(target) == int:
            target = str(target)
        cmd = ['nodemgr', 'set', '--param', keyname, '--value', target]
        pinfo(' '.join(cmd))
        p = Popen(cmd, stdout=PIPE, stderr=PIPE)
        out, err = p.communicate()
        return p.returncode

    def get_val(self, keyname):
        cmd = ['nodemgr', 'get', '--param', keyname]
        p = Popen(cmd, stdout=PIPE, stderr=PIPE)
        out, err = p.communicate()
        if p.returncode != 0:
            #perror('\n'.join((' '.join(cmd), out, err)))
            return
        if "deprecated" in bdecode(err):
            return
        out = bdecode(out).strip()
        try:
            out = int(out)
        except:
            pass
        return out

    def _check_key(self, keyname, target, op, value, verbose=True):
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
        elif op == 'unset':
            if verbose:
                perror("%s=%s value must be unset"%(keyname, str(value)))
            r |= RET_ERR
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

        if op not in ('>=', '<=', '=', 'unset'):
            if verbose:
                perror("'value' list member 0 must be either '=', '>=', '<=' or unset: %s"%str(key))
            return RET_NA

        keyname = key['key']
        value = self.get_val(keyname)

        if value is None:
            if op == 'unset':
                 if verbose:
                     pinfo("%s key is not set"%keyname)
                 return RET_OK
            else:
                 if verbose:
                     perror("%s key is not set"%keyname)
                 return RET_ERR

        return self._check_key(keyname, target, op, value, verbose)

    def fix_key(self, key):
        if 'op' not in key:
            op = "="
        else:
            op = key['op']
        if op == "unset":
            return self.unset_val(key['key'])
        else:
            return self.set_val(key['key'], key['value'])

    def check(self):
        r = 0
        for key in self.keys:
            r |= self.check_key(key, verbose=True)
        return r

    def fix(self):
        r = 0
        for key in self.keys:
            if self.check_key(key, verbose=False) == RET_ERR:
                r += self.fix_key(key)
        return r

if __name__ == "__main__":
    main(NodeConf)

