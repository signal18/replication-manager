#!/usr/bin/env python

data = {
  "default_prefix": "OSVC_COMP_XINETD_",
  "example_value": """
{
  "gssftp": {
    "disable": "no",
    "server_args": "-l -a -u 022"
  }
}""",
  "description": """* Setup and verify a xinetd service configuration
""",
  "form_definition": """
Desc: |
  A rule defining how a xinetd service should be configured

Inputs:
  -
    Id: xinetdsvc
    Label: Service Name
    DisplayModeLabel: service
    LabelCss: action16
    Mandatory: Yes
    Help: The xinetd service name, ie the service file name in /etc/xinetd.d/
    Type: string
  -
    Id: disable
    Label: Disable 
    DisplayModeLabel: Disable
    LabelCss: action16
    Help: Defines if the xinetd service target state is enabled or disabled
    Type: string
    Default: yes
    Candidates:
      - "yes"
      - "no"
  -
    Id: server_args
    Label: Server Args
    DisplayModeLabel: args
    LabelCss: action16
    Help: Command line parameter to pass to the service's server executable
    Type: string
""",
}

import os
import sys
import json
import pwd

sys.path.append(os.path.dirname(__file__))

from comp import *

class Xinetd(CompObject):
    def __init__(self, prefix=None):
        CompObject.__init__(self, prefix=prefix, data=data)

    def init(self):
        self.base = os.path.join(os.sep, "etc", "xinetd.d")
        if not os.path.exists(self.base):
            perror(self.base, 'does not exist')
            raise NotApplicable()

        self.svcs = {}
        for d in self.get_rules():
            self.svcs.update(d)

        if len(self.svcs) == 0:
            raise NotApplicable()

        self.cf_d = {}
        self.known_props = (
            "flags",
            "socket_type",
            "wait",
            "user",
            "server",
            "server_args",
            "disable")

    def fixable(self):
        return RET_NA

    def get_svc(self, svc):
        if svc in self.cf_d:
            return self.cf_d[svc]

        p = os.path.join(self.base, svc)
        if not os.path.exists(p):
            self.cf_d[svc] = {}
            return self.cf_d[svc]

        if svc not in self.cf_d:
            self.cf_d[svc] = {}

        with open(p, 'r') as f:
            for line in f.read().split('\n'):
                if '=' not in line:
                    continue
                l = line.split('=')
                if len(l) != 2:
                    continue
                var = l[0].strip()
                val = l[1].strip()
                self.cf_d[svc][var] = val

        return self.cf_d[svc]

    def fix_item(self, svc, item, target):
        if item not in self.known_props:
            perror('xinetd service', svc, item+': unknown property in compliance rule')
            return RET_ERR
        cf = self.get_svc(svc)

        if item in cf and cf[item] == target:
            return RET_OK

        p = os.path.join(self.base, svc)
        if not os.path.exists(p):
            perror(p, "does not exist")
            return RET_ERR

        done = False
        with open(p, 'r') as f:
            buff = f.read().split('\n')
            for i, line in enumerate(buff):
                if '=' not in line:
                    continue
                l = line.split('=')
                if len(l) != 2:
                    continue
                var = l[0].strip()
                if var != item:
                    continue
                l[1] = target
                buff[i] = "= ".join(l)
                done = True

        if not done:
            with open(p, 'r') as f:
                buff = f.read().split('\n')
                for i, line in enumerate(buff):
                    if '=' not in line:
                        continue
                    l = line.split('=')
                    if len(l) != 2:
                        continue
                    buff.insert(i, item+" = "+target)
                    done = True
                    break

        if not done:
            perror("failed to set", item, "=", target, "in", p)
            return RET_ERR

        with open(p, 'w') as f:
            f.write("\n".join(buff))

        pinfo("set", item, "=", target, "in", p)
        return RET_OK

    def check_item(self, svc, item, target, verbose=False):
        if item not in self.known_props:
            perror('xinetd service', svc, item+': unknown property in compliance rule')
            return RET_ERR
        cf = self.get_svc(svc)
        if item in cf and target == cf[item]:
            if verbose:
                pinfo('xinetd service', svc, item+':', cf[item])
            return RET_OK
        elif item in cf:
            if verbose:
                perror('xinetd service', svc, item+':', cf[item], 'target:', target)
        else:
            if verbose:
                perror('xinetd service', svc, item+': unset', 'target:', target)
        return RET_ERR

    def check_svc(self, svc, props):
        r = 0
        for prop in props:
            r |= self.check_item(svc, prop, props[prop], verbose=True)
        return r

    def fix_svc(self, svc, props):
        r = 0
        for prop in props:
            r |= self.fix_item(svc, prop, props[prop])
        return r

    def check(self):
        r = 0
        for svc, props in self.svcs.items():
            r |= self.check_svc(svc, props)
        return r

    def fix(self):
        r = 0
        for svc, props in self.svcs.items():
            r |= self.fix_svc(svc, props)
        return r

if __name__ == "__main__":
    main(Xinetd)
