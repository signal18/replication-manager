#!/usr/bin/env python

data = {
  "default_prefix": "OSVC_COMP_GROUP_",
  "example_value": """
{
  "tibco": {
    "gid": 1000,
  },
  "tibco1": {
    "gid": 1001,
  }
}
""",
  "description": """* Verify a local system group configuration
* A minus (-) prefix to the group name indicates the user should not exist

""",
  "form_definition": """
Desc: |
  A rule defining a list of Unix groups and their properties. Used by the groups compliance objects.
Css: comp48
Outputs:
  -
    Dest: compliance variable
    Type: json
    Format: dict of dict
    Key: group
    EmbedKey: No
    Class: group
Inputs:
  -
    Id: group
    Label: Group name
    DisplayModeLabel: group
    LabelCss: guys16
    Mandatory: Yes
    Type: string
    Help: The Unix group name.
  -
    Id: gid
    Label: Group id
    DisplayModeLabel: gid
    LabelCss: guys16
    Type: string or integer
    Help: The Unix gid of this group.
""",
}

import os
import sys
import json
import grp
import re
from subprocess import Popen 

sys.path.append(os.path.dirname(__file__))

from comp import *

blacklist = [
 "root",
 "bin",
 "daemon",
 "sys",
 "adm",
 "tty",
 "disk",
 "lp",
 "mem",
 "kmem",
 "wheel",
 "mail",
 "uucp",
 "man",
 "games",
 "gopher",
 "video",
 "dip",
 "ftp",
 "lock",
 "audio",
 "nobody",
 "users",
 "utmp",
 "utempter",
 "floppy",
 "vcsa",
 "cdrom",
 "tape",
 "dialout",
 "saslauth",
 "postdrop",
 "postfix",
 "sshd",
 "opensvc",
 "mailnull",
 "smmsp",
 "slocate",
 "rpc",
 "rpcuser",
 "nfsnobody",
 "tcpdump",
 "ntp"
]

class CompGroup(CompObject):
    def __init__(self, prefix=None):
        CompObject.__init__(self, prefix=prefix, data=data)

    def init(self):
        self.grt = {
            'gid': 'gr_gid',
        }

        self.groupmod_p = {
            'gid': '-g',
        }

        self.sysname, self.nodename, x, x, self.machine = os.uname()

        if self.sysname == "FreeBSD":
            self.groupadd = ["pw", "groupadd"]
            self.groupmod = ["pw", "groupmod"]
            self.groupdel = ["pw", "groupdel"]
        elif self.sysname == 'AIX':
            self.groupmod = ['chgroup']
            self.groupadd = ['mkgroup']
            self.groupdel = ['rmgroup']
            self.groupmod_p = {
                'gid': 'id',
            }
        else:
            self.groupadd = ["groupadd"]
            self.groupmod = ["groupmod"]
            self.groupdel = ["groupdel"]

        if self.sysname not in ['SunOS', 'Linux', 'HP-UX', 'AIX', 'OSF1', 'FreeBSD']:
            perror('group: module not supported on', self.sysname)
            raise NotApplicable

        self.groups = {}
        for d in self.get_rules():
            self.groups.update(d)

        for group, d in self.groups.items():
            for k in ('uid', 'gid'):
                if k in d:
                    self.groups[group][k] = int(d[k])

    def fixable(self):
        return RET_NA

    def fmt_opt_gen(self, item, target):
        return [item, target]

    def fmt_opt_aix(self, item, target):
        return ['='.join((item, target))]

    def fmt_opt(self, item, target):
        if self.sysname == 'AIX':
            return self.fmt_opt_aix(item, target)
        else:
            return self.fmt_opt_gen(item, target)
        
    def fix_item(self, group, item, target):
        if item in self.groupmod_p:
            cmd = [] + self.groupmod
            if self.sysname == "FreeBSD":
                cmd += [group]
            cmd += self.fmt_opt(self.groupmod_p[item], str(target))
            if self.sysname != "FreeBSD":
                cmd += [group]
            pinfo("group:", ' '.join(cmd))
            p = Popen(cmd)
            out, err = p.communicate()
            r = p.returncode
            if r == 0:
                return RET_OK
            else:
                return RET_ERR
        else:
            perror('group: no fix implemented for', item)
            return RET_ERR

    def check_item(self, group, item, target, current, verbose=False):
        if type(current) == int and current < 0:
            current += 4294967296
        if target == current:
            if verbose:
                pinfo('group', group, item+':', current)
            return RET_OK
        else:
            if verbose:
                perror('group', group, item+':', current, 'target:', target)
            return RET_ERR 

    def try_create_group(self, props):
        #
        # don't try to create group if passwd db is not 'files'
        # beware: 'files' db is the implicit default
        #
        if 'db' in props and props['db'] != 'files':
            return False
        if set(self.grt.keys()) <= set(props.keys()):
            return True
        return False

    def check_group_del(self, group):
        try:
            groupinfo = grp.getgrnam(group)
        except KeyError:
            pinfo('group', group, 'does not exist, on target')
            return RET_OK
        perror('group', group, "exists, shouldn't")
        return RET_ERR

    def check_group(self, group, props):
        if group.startswith('-'):
            return self.check_group_del(group.lstrip('-'))
        r = 0
        try:
            groupinfo = grp.getgrnam(group)
        except KeyError:
            if self.try_create_group(props):
                perror('group', group, 'does not exist')
                return RET_ERR
            else:
                pinfo('group', group, 'does not exist and not enough info to create it')
                return RET_OK
        for prop in self.grt:
            if prop in props:
                r |= self.check_item(group, prop, props[prop], getattr(groupinfo, self.grt[prop]), verbose=True)
        return r

    def create_group(self, group, props):
        cmd = [] + self.groupadd
        if self.sysname == "FreeBSD":
            cmd += [group]
        for item in self.grt:
            cmd += self.fmt_opt(self.groupmod_p[item], str(props[item]))
        if self.sysname != "FreeBSD":
            cmd += [group]
        pinfo("group:", ' '.join(cmd))
        p = Popen(cmd)
        out, err = p.communicate()
        r = p.returncode
        if r == 0:
            return RET_OK
        else:
            return RET_ERR

    def fix_group_del(self, group):
        if group in blacklist:
            perror("group", group, "... cowardly refusing to delete")
            return RET_ERR
        try:
            groupinfo = grp.getgrnam(group)
        except KeyError:
            return RET_OK
        cmd = self.groupdel + [group]
        pinfo("group:", ' '.join(cmd))
        p = Popen(cmd)
        out, err = p.communicate()
        r = p.returncode
        if r == 0:
            return RET_OK
        else:
            return RET_ERR

    def fix_group(self, group, props):
        if group.startswith('-'):
            return self.fix_group_del(group.lstrip('-'))
        r = 0
        try:
            groupinfo = grp.getgrnam(group)
        except KeyError:
            if self.try_create_group(props):
                return self.create_group(group, props)
            else:
                perror('group', group, 'does not exist')
                return RET_OK
        for prop in self.grt:
            if prop in props and \
               self.check_item(group, prop, props[prop], getattr(groupinfo, self.grt[prop])) != RET_OK:
                r |= self.fix_item(group, prop, props[prop])
        return r

    def check(self):
        r = 0
        for group, props in self.groups.items():
            r |= self.check_group(group, props)
        return r

    def fix(self):
        r = 0
        for group, props in self.groups.items():
            r |= self.fix_group(group, props)
        return r

if __name__ == "__main__":
    main(CompGroup)
