#!/usr/bin/env python

data = {
  "default_prefix": "OSVC_COMP_GROUP_",
  "example_value": """
{
  "tibco": {
    "members": ["tibco", "tibco1"]
  },
  "tibco1": {
    "members": ["tibco1"]
  }
}
""",
  "description": """* Verify a local system group configuration
* A minus (-) prefix to the group name indicates the user should not exist

""",
  "form_definition": """
Desc: |
  A rule defining a list of Unix groups and their user membership. The referenced users and groups must exist.
Css: comp48

Outputs:
  -
    Dest: compliance variable
    Type: json
    Format: dict of dict
    Key: group
    EmbedKey: No
    Class: group_membership
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
    Id: members
    Label: Group members
    DisplayModeLabel: members
    LabelCss: guy16
    Type: list of string
    Help: A comma-separed list of Unix user names members of this group.
""",
}

import os
import sys
import json
import grp
from subprocess import *
from utilities import which

sys.path.append(os.path.dirname(__file__))

from comp import *

class CompGroupMembership(CompObject):
    def __init__(self, prefix=None):
        CompObject.__init__(self, prefix=prefix, data=data)

    def init(self):
        self.member_of_h = {}
        self.grt = {
            'members': 'gr_mem',
        }
        self.sysname, self.nodename, x, x, self.machine = os.uname()

        if self.sysname not in ['SunOS', 'Linux', 'HP-UX', 'AIX', 'OSF1']:
            perror('group_membership: compliance object not supported on', self.sysname)
            raise NotApplicable

        self.groups = {}
        for d in self.get_rules():
            if type(d) != dict:
                continue
            for k, v in d.items():
                if "members" not in v:
                    continue
                for i, m in enumerate(v["members"]):
                    d[k]["members"][i] = m.strip()
            self.groups.update(d)

        if os.path.exists('/usr/xpg4/bin/id'):
            self.id_bin = '/usr/xpg4/bin/id'
        else:
            self.id_bin = 'id'

    def get_primary_group(self, user):
        cmd = [self.id_bin, "-gn", user]
        p = Popen(cmd, stdout=PIPE, stderr=PIPE)
        out, err = p.communicate()
        if p.returncode != 0:
            return
        return out.strip()

    def member_of(self, user, refresh=False):
        if not refresh and user in self.member_of_h:
            # cache hit
            return self.member_of_h[user]

        eg = self.get_primary_group(user)
        if eg is None:
            self.member_of_h[user] = []
            return []

        cmd = [self.id_bin, "-Gn", user]
        p = Popen(cmd, stdout=PIPE, stderr=PIPE)
        out, err = p.communicate()
        if p.returncode != 0:
            self.member_of_h[user] = []
            return self.member_of_h[user]
        ag = set(out.strip().split())
        ag -= set([eg])
        self.member_of_h[user] = ag
        return self.member_of_h[user]

    def fixable(self):
        return RET_NA

    def del_member(self, group, user):
        ag = self.member_of(user)
        if len(ag) == 0:
            return 0
        g = ag - set([group])
        g = ','.join(g)
        return self.fix_member(g, user)

    def add_member(self, group, user):
        if 0 != self._check_member_accnt(user):
            perror('group', group+':', 'cannot add inexistant user "%s"'%user)
            return RET_ERR
        if self.get_primary_group(user) == group:
            pinfo("group %s is already the primary group of user %s: skip declaration as a secondary group (you may want to change your rule)" % (group, user))
            return RET_OK
        ag = self.member_of(user)
        g = ag | set([group])
        g = ','.join(g)
        return self.fix_member(g, user)

    def fix_member(self, g, user):
        cmd = ['usermod', '-G', g, user]
        pinfo("group_membership:", ' '.join(cmd))
        p = Popen(cmd)
        out, err = p.communicate()
        r = p.returncode
        ag = self.member_of(user, refresh=True)
        if r == 0:
            return RET_OK
        else:
            return RET_ERR

    def fix_members(self, group, target):
        r = 0
        for user in target:
            if group in self.member_of(user):
                continue
            r += self.add_member(group, user)
        return r

    def fix_item(self, group, item, target):
        if item == 'members':
            return self.fix_members(group, target)
        else:
            perror("group_membership:", 'no fix implemented for', item)
            return RET_ERR

    def _check_member_accnt(self, user):
        if which('getent'):
            xcmd = ['getent', 'passwd', user]
        elif which('pwget'):
            xcmd = ['pwget', '-n', user]
        else:
            return 0
        xp = Popen(xcmd, stdout=PIPE, stderr=PIPE, close_fds=True)
        xout, xerr = xp.communicate()
        return xp.returncode

    def _check_members_accnts(self, group, user_list, which, verbose):
        r = RET_OK
        for user in user_list:
            rc = self._check_member_accnt(user)
            if rc != 0:
                r |= RET_ERR
                if verbose:
                    perror('group', group, '%s member "%s" does not exist'%(which, user))
        return r

    def filter_target(self, group, target):
        new_target = []
        for user in target:
            pg = self.get_primary_group(user)
            if pg == group:
                continue
            new_target.append(user)
        discarded = set(target)-set(new_target)
        if len(discarded) > 0:
            pinfo("group %s members discarded: %s, as they already use this group as primary (you may want to change your rule)" % (group, ', '.join(discarded)))
        return new_target
                
    def check_item(self, group, item, target, current, verbose=False):
        r = RET_OK
        if item == 'members':
            r |= self._check_members_accnts(group, current, 'existing', verbose)
            r |= self._check_members_accnts(group, target, 'target', verbose)
        if not isinstance(current, list):
            current = [current]
        target = self.filter_target(group, target)
        if set(target) <= set(current):
            if verbose:
                pinfo('group', group, item+':', ', '.join(current))
            return r
        else:
            if verbose:
                perror('group', group, item+':', ', '.join(current), '| target:', ', '.join(target))
            return r|RET_ERR

    def check_group(self, group, props):
        r = 0
        try:
            groupinfo = grp.getgrnam(group)
        except KeyError:
            pinfo('group', group, 'does not exist')
            return RET_OK
        for prop in self.grt:
            if prop in props:
                r |= self.check_item(group, prop, props[prop], getattr(groupinfo, self.grt[prop]), verbose=True)
        return r

    def fix_group(self, group, props):
        r = 0
        try:
            groupinfo = grp.getgrnam(group)
        except KeyError:
            pinfo('group', group, 'does not exist')
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
    main(CompGroupMembership)
