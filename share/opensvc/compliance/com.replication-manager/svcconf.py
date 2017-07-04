#!/usr/bin/env python

data = {
  "default_prefix": "OSVC_COMP_GROUP_",
  "example_env": {
    "OSVC_COMP_SERVICES_SVCNAME": "testsvc",
  },
  "example_value": """
[
  {
    "value": "fd5373b3d938",
    "key": "container#1.run_image",
    "op": "="
  },
  {
    "value": "/bin/sh",
    "key": "container#1.run_command",
    "op": "="
  },
  {
    "value": "/opt/%%ENV:SERVICES_SVCNAME%%",
    "key": "DEFAULT.docker_data_dir",
    "op": "="
  },
  {
    "value": "no",
    "key": "container(type=docker).disable",
    "op": "="
  },
  {
    "value": 123,
    "key": "container(type=docker&&run_command=/bin/sh).newvar",
    "op": "="
  }
]
""",
  "description": """* Setup and verify parameters in a opensvc service configuration.

""",
  "form_definition": """
Desc: |
  A rule to set a parameter in OpenSVC <service>.conf configuration file. Used by the 'svcconf' compliance object.
Css: comp48
Outputs:
  -
    Dest: compliance variable
    Type: json
    Format: list of dict
    Class: svcconf
Inputs:
  -
    Id: key
    Label: Key
    DisplayModeLabel: key
    LabelCss: action16
    Mandatory: Yes
    Type: string
    Help: The OpenSVC <service>.conf parameter to check.
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
    Help: The OpenSVC <service>.conf parameter value to check.

""",
}


import os
import sys
import json
import re
import copy
from subprocess import *

sys.path.append(os.path.dirname(__file__))

from comp import *

class SvcConf(CompObject):
    def __init__(self, prefix=None):
        CompObject.__init__(self, prefix=prefix, data=data)

    def init(self):
        self.keys = []

        if "OSVC_COMP_SERVICES_SVCNAME" not in os.environ:
            pinfo("SERVICES_SVCNAME is not set")
            raise NotApplicable()

        self.svcname = os.environ['OSVC_COMP_SERVICES_SVCNAME']

        self.keys = self.get_rules()

        try:
            self.get_config_file(refresh=True)
        except Exception as e:
            perror("unable to load service configuration:", str(e))
            raise ComplianceError()

        self.sanitize_keys()
        self.expand_keys()

    def get_config_file(self, refresh=False):
       if not refresh:
           return self.svc_config
       cmd = ['svcmgr', '-s', self.svcname, 'json_config']
       p = Popen(cmd, stdout=PIPE, stderr=PIPE)
       out, err = p.communicate()
       out = bdecode(out)
       self.svc_config = json.loads(out)
       return self.svc_config

    def fixable(self):
        return RET_NA

    def set_val(self, keyname, target):
        if type(target) == int:
            target = str(target)
        cmd = ['svcmgr', '-s', self.svcname, 'set', '--param', keyname, '--value', target]
        pinfo(' '.join(cmd))
        p = Popen(cmd, stdout=PIPE, stderr=PIPE)
        out, err = p.communicate()
        return p.returncode

    def get_val(self, keyname):
        section, var = keyname.split('.')
        if section not in self.svc_config:
            return None
        return self.svc_config[section].get(var)

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

    def check_filter(self, section, filter):
        op = None
        i = 0
        try:
            i = filter.index("&&")
            op = "and"
        except ValueError:
            pass
        try:
            i = filter.index("||")
            op = "or"
        except ValueError:
            pass

        if i == 0:
            _filter = filter
            _tail = ""
        else:
            _filter = filter[:i]
            _tail = filter[i:].lstrip("&&").lstrip("||")

        r = self._check_filter(section, _filter)
        #pinfo(" _check_filter('%s', '%s') => %s" % (section, _filter, str(r)))

        if op == "and":
            r &= self.check_filter(section, _tail)
        elif op == "or":
            r |= self.check_filter(section, _tail)

        return r

    def _check_filter(self, section, filter):
        if "~=" in filter:
            return self._check_filter_reg(section, filter)
        elif "=" in filter:
            return self._check_filter_eq(section, filter)
        perror("invalid filter syntax: %s" % filter)
        return False

    def _check_filter_eq(self, section, filter):
        l = filter.split("=")
        if len(l) != 2:
            perror("invalid filter syntax: %s" % filter)
            return False
        key, val = l
        cur_val = self.svc_config[section].get(key)
        if cur_val is None:
            return False
        if str(cur_val) == str(val):
            return True
        return False

    def _check_filter_reg(self, section, filter):
        l = filter.split("~=")
        if len(l) != 2:
            perror("invalid filter syntax: %s" % filter)
            return False
        key, val = l
        val = val.strip("/")
        cur_val = self.svc_config[section].get(key)
        if cur_val is None:
            return False
        reg = re.compile(val)
        if reg.match(cur_val):
            return True
        return False

    def resolve_sections(self, s, filter):
        """
        s is a ressource section name (fs, container, app, sync, ...)
        filter is a regexp like expression
           container(type=docker)
           fs(mnt~=/.*tools/)
           container(type=docker&&run_image~=/opensvc\/collector_web:build.*/)
           fs(mnt~=/.*tools/||mnt~=/.*moteurs/)
        """
        result = [];
        eligiblesections = [];
        for section in self.svc_config.keys():
            if section.startswith(s+'#') or section == s:
                eligiblesections.append(section)
        for section in eligiblesections:
            if self.check_filter(section, filter):
                #pinfo("   =>", section, "matches filter")
                result.append(section)
        result.sort()
        return result

    def sanitize_keys(self, verbose=True):
        r = RET_OK
        for key in self.keys:
            if 'key' not in key:
                if verbose:
                    perror("'key' not set in rule %s"%str(key))
                r |= RET_NA
            if 'value' not in key:
                if verbose:
                    perror("'value' not set in rule %s"%str(key))
                r |= RET_NA
            if 'op' not in key:
                op = "="
            else:
                op = key['op']

            if op not in ('>=', '<=', '='):
                if verbose:
                    perror("'value' list member 0 must be either '=', '>=' or '<=': %s"%str(key))
                r |= RET_NA

        if r is not RET_OK:
            sys.exit(r)

    def expand_keys(self):
        expanded_keys = []

        for key in self.keys:
            keyname = key['key']
            target = key['value']
            op = key['op']
            sectionlist = [];
            reg1 = re.compile(r'(.*)\((.*)\)\.(.*)')
            reg2 = re.compile(r'(.*)\.(.*)')
            m = reg1.search(keyname)
            if m:
                section = m.group(1)
                filter = m.group(2)
                var = m.group(3)
                sectionlist = self.resolve_sections(section, filter)
                for resolvedsection in sectionlist:
                    newdict = {
                     'key': '.'.join([resolvedsection, var]),
                     'op': op,
                     'value': target
                    }
                    expanded_keys.append(newdict)
                continue
            m = reg2.search(keyname)
            if m:
                section = m.group(1)
                var = m.group(2)
                expanded_keys.append(copy.copy(key))
                continue

            # drop key

        self.keys = expanded_keys

    def check_key(self, key, verbose=True):
        op = key['op']
        target = key['value']
        keyname = key['key']

        value = self.get_val(keyname)

        if value is None:
            if verbose:
                perror("%s key is not set"%keyname)
            return RET_ERR

        return self._check_key(keyname, target, op, value, verbose)

    def fix_key(self, key):
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
    main(SvcConf)
