#!/usr/bin/env python

data = {
  "default_prefix": "OSVC_COMP_USER_",
  "example_value": """
{
  "tibco": {
    "shell": "/bin/ksh",
    "gecos":"agecos"
  },
  "tibco1": {
    "shell": "/bin/tcsh",
    "gecos": "another gecos"
  }
}
""",
  "description": """* Verify a local system user configuration
* A minus (-) prefix to the user name indicates the user should not exist

Environment variable modifying the object behaviour:
* OSVC_COMP_USERS_INITIAL_PASSWD=true|false
""",
  "form_definition": """
Desc: |
  A rule defining a list of Unix users and their properties. Used by the users and group_membership compliance objects.
Css: comp48
Outputs:
  -
    Dest: compliance variable
    Type: json
    Format: dict of dict
    Key: user
    EmbedKey: No
    Class: user
Inputs:
  -
    Id: user
    Label: User name
    DisplayModeLabel: user
    LabelCss: guy16
    Mandatory: Yes
    Type: string
    Help: The Unix user name.
  -
    Id: uid
    Label: User id
    DisplayModeLabel: uid
    LabelCss: guy16
    Mandatory: Yes
    Type: string or integer
    Help: The Unix uid of this user.
  -
    Id: gid
    Label: Group id
    DisplayModeLabel: gid
    LabelCss: guys16
    Mandatory: Yes
    Type: string or integer
    Help: The Unix principal gid of this user.
  -
    Id: shell
    Label: Login shell
    DisplayModeLabel: shell
    LabelCss: action16
    Type: string
    Help: The Unix login shell for this user.
  -
    Id: home
    Label: Home directory
    DisplayModeLabel: home
    LabelCss: action16
    Type: string
    Help: The Unix home directory full path for this user.
  -
    Id: password
    Label: Password hash
    DisplayModeLabel: pwd
    LabelCss: action16
    Type: string
    Help: The password hash for this user. It is recommanded to set it to '!!' or to set initial password to change upon first login. Leave empty to not check nor set the password.
  -
    Id: gecos
    Label: Gecos
    DisplayModeLabel: gecos
    LabelCss: action16
    Type: string
    Help: A one-line comment field describing the user.
  -
    Id: check_home
    Label: Enforce homedir ownership
    DisplayModeLabel: home ownership
    LabelCss: action16
    Type: string
    Default: yes
    Candidates:
      - "yes"
      - "no"
    Help: Toggles the user home directory ownership checking.
""",
}

import os
import sys
import json
import pwd
import re
from utilities import which

try:
    import spwd
    cap_shadow = True
except:
    cap_shadow = False

from subprocess import Popen, list2cmdline, PIPE

sys.path.append(os.path.dirname(__file__))

from comp import *

blacklist = [
 "root",
 "bin",
 "daemon",
 "adm",
 "lp",
 "sync",
 "shutdown",
 "halt",
 "mail",
 "news",
 "uucp",
 "operator",
 "nobody",
 "nscd",
 "vcsa",
 "pcap",
 "mailnull",
 "smmsp",
 "sshd",
 "rpc",
 "avahi",
 "rpcuser",
 "nfsnobody",
 "haldaemon",
 "avahi-autoipd",
 "ntp"
]

class CompUser(CompObject):
    def __init__(self, prefix=None):
        CompObject.__init__(self, prefix=prefix, data=data)

    def init(self):
        self.pwt = {
            'shell': 'pw_shell',
            'home': 'pw_dir',
            'uid': 'pw_uid',
            'gid': 'pw_gid',
            'gecos': 'pw_gecos',
            'password': 'pw_passwd',
        }
        self.spwt = {
            'spassword': 'sp_pwd',
        }
        self.usermod_p = {
            'shell': '-s',
            'home': '-d',
            'uid': '-u',
            'gid': '-g',
            'gecos': '-c',
            'password': '-p',
            'spassword': '-p',
        }
        self.sysname, self.nodename, x, x, self.machine = os.uname()

        if "OSVC_COMP_USERS_INITIAL_PASSWD" in os.environ and \
           os.environ["OSVC_COMP_USERS_INITIAL_PASSWD"] == "true":
            self.initial_passwd = True
        else:
            self.initial_passwd = False

        if self.sysname not in ['SunOS', 'Linux', 'HP-UX', 'AIX', 'OSF1', 'FreeBSD']:
            perror('module not supported on', self.sysname)
            raise NotApplicable()

        if self.sysname == "FreeBSD":
            self.useradd = ["pw", "useradd"]
            self.usermod = ["pw", "usermod"]
            self.userdel = ["pw", "userdel"]
        else:
            self.useradd = ["useradd"]
            self.usermod = ["usermod"]
            self.userdel = ["userdel"]

        self.users = {}
        for d in self.get_rules():
            for user in d:
                if user not in self.users:
                    self.users[user] = d[user]
                else:
                    for key in self.usermod_p.keys():
                        if key in d[user] and key not in self.users[user]:
                            self.users[user][key] = d[user][key]

        for user, d in self.users.items():
            for k in ('uid', 'gid'):
                if k in self.users[user]:
                    self.users[user][k] = int(d[k])

            if "password" in d and len(d["password"]) == 0:
                del(self.users[user]["password"])

            if cap_shadow:
                if "password" in d and len(d["password"]) > 0 and \
                   ("spassword" not in d or len(d["spassword"]) == 0):
                    self.users[user]["spassword"] = self.users[user]["password"]
                    del self.users[user]["password"]
                if "spassword" not in d:
                    self.users[user]["spassword"] = "x"
            else:
                if "spassword" in d and len(d["spassword"]) > 0 and \
                   ("password" not in d or len(d["password"]) == 0):
                    self.users[user]["password"] = self.users[user]["spassword"]
                    del self.users[user]["spassword"]
                if "password" not in d:
                    self.users[user]["password"] = "x"


    def fixable(self):
        if not which(self.usermod[0]):
            perror(self.usermod[0], "program not found")
            return RET_ERR
        return RET_OK

    def grpconv(self):
        if not cap_shadow or not os.path.exists('/etc/gshadow'):
            return
        if not which('grpconv'):
            return
        with open('/etc/group', 'r') as f:
            buff = f.read()
        l = []
        for line in buff.split('\n'):
            u = line.split(':')[0]
            if u in l:
                perror("duplicate group %s in /etc/group. skip grpconv (grpconv bug workaround)"%u)
                return
            l.append(u)
        p = Popen(['grpconv'])
        p.communicate()

    def pwconv(self):
        if not cap_shadow or not os.path.exists('/etc/shadow'):
            return
        if not which('pwconv'):
            return
        p = Popen(['pwconv'])
        p.communicate()

    def fix_item(self, user, item, target):
        if item in ["password", "spassword"]:
            if self.initial_passwd:
                pinfo("skip", user, "password modification in initial_passwd mode")
                return RET_OK
            if target == "x":
                return RET_OK
            if self.sysname in ("AIX"):
                return RET_OK
        cmd = [] + self.usermod
        if self.sysname == "FreeBSD":
            cmd.append(user)
        cmd += [self.usermod_p[item], str(target)]
        if item == 'home':
            cmd.append('-m')
        if self.sysname != "FreeBSD":
            cmd.append(user)
        pinfo(list2cmdline(cmd))
        p = Popen(cmd)
        out, err = p.communicate()
        r = p.returncode
        self.pwconv()
        self.grpconv()
        if r == 0:
            return RET_OK
        else:
            return RET_ERR

    def check_item(self, user, item, target, current, verbose=False):
        if type(current) == int and current < 0:
            current += 4294967296
        if sys.version_info[0] < 3 and type(current) == str and type(target) == unicode:
            current = unicode(current, errors="ignore")
        if target == current:
            if verbose:
                pinfo('user', user, item+':', current)
            return RET_OK
        elif "passw" in item and target == "!!" and current == "":
            if verbose:
                pinfo('user', user, item+':', current)
            return RET_OK
        else:
            if verbose:
                perror('user', user, item+':', current, 'target:', target)
            return RET_ERR

    def check_user_del(self, user, verbose=True):
        r = 0
        try:
            userinfo=pwd.getpwnam(user)
        except KeyError:
            if verbose:
                pinfo('user', user, 'does not exist, on target')
            return RET_OK
        if verbose:
            perror('user', user, "exists, shouldn't")
        return RET_ERR

    def check_user(self, user, props, verbose=True):
        if user.startswith('-'):
            return self.check_user_del(user.lstrip('-'), verbose=verbose)
        r = 0
        try:
            userinfo=pwd.getpwnam(user)
        except KeyError:
            if self.try_create_user(props):
                if verbose:
                    perror('user', user, 'does not exist')
                return RET_ERR
            else:
                if verbose:
                    perror('user', user, 'does not exist and not enough info to create it')
                return RET_ERR

        for prop in self.pwt:
            if prop in props:
                if prop == "password":
                    if self.initial_passwd:
                        if verbose:
                            pinfo("skip", user, "passwd checking in initial_passwd mode")
                        continue
                    if props[prop] == "x":
                        continue
                r |= self.check_item(user, prop, props[prop], getattr(userinfo, self.pwt[prop]), verbose=verbose)

        if 'check_home' not in props or props['check_home'] == "yes":
            r |= self.check_home_ownership(user, verbose=verbose)

        if not cap_shadow:
            return r

        try:
            usersinfo=spwd.getspnam(user)
        except KeyError:
            if "spassword" in props:
                if verbose:
                    perror(user, "not declared in /etc/shadow")
                r |= RET_ERR
            usersinfo = None

        if usersinfo is not None:
            for prop in self.spwt:
                if prop in props:
                    if prop == "spassword":
                        if self.initial_passwd:
                            if verbose:
                                pinfo("skip", user, "spasswd checking in initial_passwd mode")
                            continue
                        if props[prop] == "x":
                            continue
                    r |= self.check_item(user, prop, props[prop], getattr(usersinfo, self.spwt[prop]), verbose=verbose)

        return r

    def try_create_user(self, props):
        #
        # don't try to create user if passwd db is not 'files'
        # beware: 'files' db is the implicit default
        #
        if 'db' in props and props['db'] != 'files':
            return False
        return True

    def get_uid(self, user):
        import pwd
        try:
            info=pwd.getpwnam(user)
            uid = info[2]
        except:
            perror("user %s does not exist"%user)
            raise ComplianceError()
        return uid

    def check_home_ownership(self, user, verbose=True):
        path = os.path.expanduser("~"+user)
        if not os.path.exists(path):
            if verbose:
                perror(path, "homedir does not exist")
            return RET_ERR 
        tuid = self.get_uid(user)
        uid = os.stat(path).st_uid
        if uid != tuid:
            if verbose: perror(path, 'uid should be %s but is %s'%(str(tuid), str(uid)))
            return RET_ERR
        if verbose: pinfo(path, 'owner is', user)
        return RET_OK

    def fix_home_ownership(self, user):
        if self.check_home_ownership(user, verbose=False) == RET_OK:
            return RET_OK
        uid = self.get_uid(user)
        path = os.path.expanduser("~"+user)
        if not os.path.exists(path):
            if os.path.exists("/etc/skel"):
                cmd = ['cp', '-R', '/etc/skel/', path]
                pinfo(list2cmdline(cmd))
                p = Popen(cmd)
                out, err = p.communicate()
                r = p.returncode
                if r != 0:
                    return RET_ERR

                cmd = ['chown', '-R', str(uid), path]
                pinfo(list2cmdline(cmd))
                p = Popen(cmd)
                out, err = p.communicate()
                r = p.returncode
                if r != 0:
                    return RET_ERR
            else:
                os.makedirs(path)
                os.chown(path, uid, -1)
        return RET_OK

    def unlock_user(self, user):
        if self.sysname != "SunOS":
            return
        cmd = ["uname", "-r"]
        p = Popen(cmd, stdout=PIPE, stderr=PIPE)
        out, err = p.communicate()
        if p.returncode != 0:
            return
        if out.strip() == '5.8':
            unlock_opt = '-d'
        else:
            unlock_opt = '-u'
        cmd = ["passwd", unlock_opt, user]
        pinfo(list2cmdline(cmd))
        p = Popen(cmd)
        out, err = p.communicate()
        r = p.returncode
        if r == 0:
            return RET_OK
        else:
            return RET_ERR

    def create_user(self, user, props):
        cmd = [] + self.useradd
        if self.sysname == "FreeBSD":
            cmd += [user]
        for item in props:
            if item == "check_home":
                continue
            prop = str(props[item])
            if len(prop) == 0:
                continue
            if item.endswith("password") and self.sysname in ("AIX", "SunOS", "OSF1"):
                continue
            cmd = cmd + self.usermod_p[item].split() + [prop]
            if item == "home":
                cmd.append("-m")
        if self.sysname != "FreeBSD":
            cmd += [user]
        pinfo(list2cmdline(cmd))
        p = Popen(cmd)
        out, err = p.communicate()
        r = p.returncode
        if r == 0:
            if self.unlock_user(user) == RET_ERR:
                return RET_ERR
            return RET_OK
        else:
            return RET_ERR

    def fix_user_del(self, user):
        if user in blacklist:
            perror("delete", user, "... cowardly refusing")
            return RET_ERR
        cmd = self.userdel + [user]
        pinfo(list2cmdline(cmd))
        p = Popen(cmd)
        out, err = p.communicate()
        r = p.returncode
        if r == 0:
            return RET_OK
        else:
            return RET_ERR

    def fix_user(self, user, props):
        if user.startswith('-'):
            return self.fix_user_del(user.lstrip('-'))
        r = 0
        try:
            userinfo = pwd.getpwnam(user)
        except KeyError:
            if self.try_create_user(props):
                return self.create_user(user, props)
            else:
                pinfo('user', user, 'does not exist and not enough info to create it')
                return RET_OK

        for prop in self.pwt:
            if prop in props and \
               self.check_item(user, prop, props[prop], getattr(userinfo, self.pwt[prop])) != RET_OK:
                r |= self.fix_item(user, prop, props[prop])

        if 'check_home' not in props or props['check_home'] == "yes":
            r |= self.fix_home_ownership(user)

        if not cap_shadow:
            return r

        try:
            usersinfo = spwd.getspnam(user)
        except KeyError:
            if "spassword" in props:
                self.fix_item(user, "spassword", props["spassword"])
                usersinfo = spwd.getspnam(user)
            else:
                usersinfo = None

        if usersinfo is not None:
            for prop in self.spwt:
                if prop in props and \
                    self.check_item(user, prop, props[prop], getattr(usersinfo, self.spwt[prop])) != RET_OK:
                    r |= self.fix_item(user, prop, props[prop])
        return r

    def check(self):
        r = 0
        for user, props in self.users.items():
            r |= self.check_user(user, props)
        return r

    def fix(self):
        r = 0
        for user, props in self.users.items():
            if self.check_user(user, props, verbose=False) == RET_ERR:
                r |= self.fix_user(user, props)
        return r

if __name__ == "__main__":
    main(CompUser)

