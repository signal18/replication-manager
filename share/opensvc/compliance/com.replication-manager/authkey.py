#!/usr/bin/env python

data = {
  "default_prefix": "OSVC_COMP_AUTHKEY_",
  "example_value": """ 
    {
      "action": "add",
      "authfile": "authorized_keys",
      "user": "testuser",
      "key": "ssh-dss AAAAB3NzaC1kc3MAAACBAPiO1jlT+5yrdPLfQ7sYF52NkfCEzT0AUUNIl+14Sbkubqe+TcU7U3taUtiDJ5YOGOzIVFIDGGtwD0AqNHQbvsiS1ywtC5BJ9362FlrpVH4o1nVZPvMxRzz5hgh3HjxqIWqwZDx29qO8Rg1/g1Gm3QYCxqPFn2a5f2AUiYqc1wtxAAAAFQC49iboZGNqssicwUrX6TUrT9H0HQAAAIBo5dNRmTF+Vd/+PI0JUOIzPJiHNKK9rnySlaxSDml9hH2LuDSjYz7BWuNP8UnPOa2pcFA4meDp5u8d5dGOWxkuYO0bLnXwDZuHtDW/ySytjwEaBLPxoqRBAyfyQNlusGsuiqDYRA7j7bS0RxINBxvDw79KdyQhuOn8/lKVG+sjrQAAAIEAoShly/JlGLQxQzPyWADV5RFlaRSPaPvFzcYT3hS+glkVd6yrCbzc30Yc8Ndu4cflQiXSZzRoUMgsy5PzuiH1M8JjwHTGNl8r9OfJpnN/OaAhMpIyA06y1ZZD9iEME3UmthFQoZnfRuE3yxi7bqyXJU4rOq04iyCTpU1UKInPdXQ= testuser"
    }
  """,
  "description": """* Installs or removes ssh public keys from authorized_key files
* Looks up the authorized_key and authorized_key2 file location in the running sshd daemon configuration.
* Add user to sshd_config AllowUser and AllowGroup if used
* Reload sshd if sshd_config has been changed
""",
  "form_definition": """
Desc: |
  Describe a list of ssh public keys to authorize login as the specified Unix user.
Css: comp48

Outputs:
  -
    Dest: compliance variable
    Type: json
    Format: dict
    Class: authkey

Inputs:
  -
    Id: action
    Label: Action
    DisplayModeLabel: action
    LabelCss: action16
    Mandatory: Yes
    Type: string
    Candidates:
      - add
      - del
    Help: Defines wether the public key must be installed or uninstalled.

  -
    Id: user
    Label: User
    DisplayModeLabel: user
    LabelCss: guy16
    Mandatory: Yes
    Type: string
    Help: Defines the Unix user name who will accept those ssh public keys.

  -
    Id: key
    Label: Public key
    DisplayModeLabel: key
    LabelCss: guy16
    Mandatory: Yes
    Type: text
    DisplayModeTrim: 60
    Help: The ssh public key as seen in authorized_keys files.

  -
    Id: authfile
    Label: Authorized keys file name
    DisplayModeLabel: authfile
    LabelCss: hd16
    Mandatory: Yes
    Candidates:
      - authorized_keys
      - authorized_keys2
    Default: authorized_keys2
    Type: string
    Help: The authorized_keys file to write the keys into.
"""
}

import os
import sys
import pwd, grp
import datetime
import shutil
from subprocess import *

sys.path.append(os.path.dirname(__file__))

from comp import *

class CompAuthKeys(CompObject):
    def __init__(self, prefix=None):
        CompObject.__init__(self, prefix=prefix, data=data)

    def init(self):
        self.authkeys = self.get_rules()

        for ak in self.authkeys:
            ak['key'] = ak['key'].replace('\n', '')

        self.installed_keys_d = {}
        self.default_authfile = "authorized_keys2"
        self.allowusers_check_done = []
        self.allowusers_fix_todo = []
        self.allowgroups_check_done = []
        self.allowgroups_fix_todo = []

    def sanitize(self, ak):
        if 'user' not in ak:
            perror("no user set in rule")
            return False
        if 'key' not in ak:
            perror("no key set in rule")
            return False
        if 'action' not in ak:
            ak['action'] = 'add'
        if 'authfile' not in ak:
            ak['authfile'] = self.default_authfile
        if ak['authfile'] not in ("authorized_keys", "authorized_keys2"):
            perror("unsupported authfile:", ak['authfile'], "(default to", self.default_authfile+")")
            ak['authfile'] = self.default_authfile
        for key in ('user', 'key', 'action', 'authfile'):
            ak[key] = ak[key].strip()
        return ak

    def fixable(self):
        return RET_NA

    def truncate_key(self, key):
        if len(key) < 50:
            s = key
        else:
            s = "'%s ... %s'" % (key[0:17], key[-30:])
        return s

    def reload_sshd(self):
        cmd = ['ps', '-ef']
        p = Popen(cmd, stdout=PIPE, stderr=PIPE)
        out, err = p.communicate()
        if p.returncode != 0:
            perror("can not find sshd process")
            return RET_ERR
        out = bdecode(out)
        for line in out.splitlines():
            if not line.endswith('sbin/sshd'):
                continue
            l = line.split()
            pid = int(l[1])
            name = l[-1]
            pinfo("send sighup to pid %d (%s)" % (pid, name))
            os.kill(pid, 1)
            return RET_OK
        perror("can not find sshd process to signal")
        return RET_ERR

    def get_sshd_config(self):
        cfs = []
        if hasattr(self, "cache_sshd_config_f"):
            return self.cache_sshd_config_f

        cmd = ['ps', '-eo', 'comm']
        p = Popen(cmd, stdout=PIPE, stderr=PIPE)
        out, err = p.communicate()
        if p.returncode == 0:
            out = bdecode(out)
            l = out.splitlines()
            if '/usr/local/sbin/sshd' in l:
                cfs.append(os.path.join(os.sep, 'usr', 'local', 'etc', 'sshd_config'))
            if '/usr/sfw/sbin/sshd' in l:
                cfs.append(os.path.join(os.sep, 'etc', 'sshd_config'))

        cfs += [os.path.join(os.sep, 'etc', 'ssh', 'sshd_config'),
                os.path.join(os.sep, 'opt', 'etc', 'sshd_config'),
                os.path.join(os.sep, 'etc', 'opt', 'ssh', 'sshd_config'),
                os.path.join(os.sep, 'usr', 'local', 'etc', 'sshd_config')]
        cf = None
        for _cf in cfs:
            if os.path.exists(_cf):
                cf = _cf
                break
        self.cache_sshd_config_f = cf
        if cf is None:
            perror("sshd_config not found")
            return None
        return cf

    def _get_authkey_file(self, key):
        if key == "authorized_keys":
            # default
            return ".ssh/authorized_keys"
        elif key == "authorized_keys2":
            key = "AuthorizedKeysFile"
        else:
            perror("unknown key", key)
            return None


        cf = self.get_sshd_config()
        if cf is None:
            perror("sshd_config not found")
            return None
        with open(cf, 'r') as f:
            buff = f.read()
        for line in buff.split('\n'):
            l = line.split()
            if len(l) != 2:
                continue
            if l[0].strip() == key:
                return l[1]
        # not found, return default
        return ".ssh/authorized_keys2"

    def get_allowusers(self):
        if hasattr(self, "cache_allowusers"):
            return self.cache_allowusers
        cf = self.get_sshd_config()
        if cf is None:
            perror("sshd_config not found")
            return None
        with open(cf, 'r') as f:
            buff = f.read()
        for line in buff.split('\n'):
            l = line.split()
            if len(l) < 2:
                continue
            if l[0].strip() == "AllowUsers":
                self.cache_allowusers = l[1:]
                return l[1:]
        self.cache_allowusers = None
        return None

    def get_allowgroups(self):
        if hasattr(self, "cache_allowgroups"):
            return self.cache_allowgroups
        cf = self.get_sshd_config()
        if cf is None:
            perror("sshd_config not found")
            return None
        with open(cf, 'r') as f:
            buff = f.read()
        for line in buff.split('\n'):
            l = line.split()
            if len(l) < 2:
                continue
            if l[0].strip() == "AllowGroups":
                self.cache_allowgroups = l[1:]
                return l[1:]
        self.cache_allowgroups = None
        return None

    def get_authkey_file(self, key, user):
        p = self._get_authkey_file(key)
        if p is None:
            return None
        p = p.replace('%u', user)
        p = p.replace('%h', os.path.expanduser('~'+user))
        p = p.replace('~', os.path.expanduser('~'+user))
        if not p.startswith('/'):
            p = os.path.join(os.path.expanduser('~'+user), p)
        return p

    def get_authkey_files(self, user):
        l = []
        p = self.get_authkey_file('authorized_keys', user)
        if p is not None:
            l.append(p)
        p = self.get_authkey_file('authorized_keys2', user)
        if p is not None:
            l.append(p)
        return l

    def get_installed_keys(self, user):
        if user in self.installed_keys_d:
            return self.installed_keys_d[user]
        else:
            self.installed_keys_d[user] = []

        ps = self.get_authkey_files(user)
        for p in ps:
            if not os.path.exists(p):
                continue
            with open(p, 'r') as f:
                self.installed_keys_d[user] += f.read().splitlines()
        return self.installed_keys_d[user]

    def get_user_group(self, user):
        gid = pwd.getpwnam(user).pw_gid
        try:
            gname = grp.getgrgid(gid).gr_name
        except KeyError:
            gname = None
        return gname

    def fix_allowusers(self, ak, verbose=True):
        self.check_allowuser(ak, verbose=False)
        if not ak['user'] in self.allowusers_fix_todo:
            return RET_OK
        self.allowusers_fix_todo.remove(ak['user'])
        au = self.get_allowusers()
        if au is None:
            return RET_OK
        l = ["AllowUsers"] + au + [ak['user']]
        s = " ".join(l)

        pinfo("adding", ak['user'], "to currently allowed users")
        cf = self.get_sshd_config()
        if cf is None:
            perror("sshd_config not found")
            return None
        with open(cf, 'r') as f:
            buff = f.read()
        lines = buff.split('\n')
        for i, line in enumerate(lines):
            l = line.split()
            if len(l) < 2:
                continue
            if l[0].strip() == "AllowUsers":
                lines[i] = s
        buff = "\n".join(lines)
        backup = cf+'.'+str(datetime.datetime.now())
        shutil.copy(cf, backup)
        with open(cf, 'w') as f:
            f.write(buff)
        self.reload_sshd()
        return RET_OK

    def fix_allowgroups(self, ak, verbose=True):
        self.check_allowgroup(ak, verbose=False)
        if not ak['user'] in self.allowgroups_fix_todo:
            return RET_OK
        self.allowgroups_fix_todo.remove(ak['user'])
        ag = self.get_allowgroups()
        if ag is None:
            return RET_OK
        ak['group'] = self.get_user_group(ak['user'])
        if ak['group'] is None:
            perror("can not set AllowGroups in sshd_config: primary group of user %s not found" % ak['user'])
            return RET_ERR
        l = ["AllowGroups"] + ag + [ak['group']]
        s = " ".join(l)

        pinfo("adding", ak['group'], "to currently allowed groups")
        cf = self.get_sshd_config()
        if cf is None:
            perror("sshd_config not found")
            return RET_ERR
        with open(cf, 'r') as f:
            buff = f.read()
        lines = buff.split('\n')
        for i, line in enumerate(lines):
            l = line.split()
            if len(l) < 2:
                continue
            if l[0].strip() == "AllowGroups":
                lines[i] = s
        buff = "\n".join(lines)
        backup = cf+'.'+str(datetime.datetime.now())
        shutil.copy(cf, backup)
        with open(cf, 'w') as f:
            f.write(buff)
        self.reload_sshd()
        return RET_OK

    def check_allowuser(self, ak, verbose=True):
        if ak['user'] in self.allowusers_check_done:
            return RET_OK
        self.allowusers_check_done.append(ak['user'])
        au = self.get_allowusers()
        if au is None:
            return RET_OK
        elif ak['user'] in au:
            if verbose:
                pinfo(ak['user'], "is correctly set in sshd AllowUsers")
            r = RET_OK
        else:
            if verbose:
                perror(ak['user'], "is not set in sshd AllowUsers")
            self.allowusers_fix_todo.append(ak['user'])
            r = RET_ERR
        return r

    def check_allowgroup(self, ak, verbose=True):
        if ak['user'] in self.allowgroups_check_done:
            return RET_OK
        self.allowgroups_check_done.append(ak['user'])
        ag = self.get_allowgroups()
        if ag is None:
            return RET_OK
        ak['group'] = self.get_user_group(ak['user'])
        if ak['group'] is None:
            if verbose:
                perror("can not determine primary group of user %s to add to AllowGroups" % ak['user'])
            return RET_ERR
        elif ak['group'] in ag:
            if verbose:
                pinfo(ak['group'], "is correctly set in sshd AllowGroups")
            r = RET_OK
        else:
            if verbose:
                perror(ak['group'], "is not set in sshd AllowGroups")
            self.allowgroups_fix_todo.append(ak['user'])
            r = RET_ERR
        return r

    def check_authkey(self, ak, verbose=True):
        ak = self.sanitize(ak)
        installed_keys = self.get_installed_keys(ak['user'])
        if ak['action'] == 'add':
            if ak['key'] not in installed_keys:
                if verbose:
                    perror('key', self.truncate_key(ak['key']), 'must be installed for user', ak['user'])
                r = RET_ERR
            else:
                if verbose:
                    pinfo('key', self.truncate_key(ak['key']), 'is correctly installed for user', ak['user'])
                r = RET_OK
        elif ak['action'] == 'del':
            if ak['key'] in installed_keys:
                if verbose:
                    perror('key', self.truncate_key(ak['key']), 'must be uninstalled for user', ak['user'])
                r = RET_ERR
            else:
                if verbose:
                    pinfo('key', self.truncate_key(ak['key']), 'is correctly not installed for user', ak['user'])
                r = RET_OK
        else:
            perror("unsupported action:", ak['action'])
            return RET_ERR
        return r

    def fix_authkey(self, ak):
        ak = self.sanitize(ak)
        if ak['action'] == 'add':
            r = self.add_authkey(ak)
            return r
        elif ak['action'] == 'del':
            return self.del_authkey(ak)
        else:
            perror("unsupported action:", ak['action'])
            return RET_ERR

    def add_authkey(self, ak):
        if self.check_authkey(ak, verbose=False) == RET_OK:
            return RET_OK

        try:
            userinfo=pwd.getpwnam(ak['user'])
        except KeyError:
            perror('user', ak['user'], 'does not exist')
            return RET_ERR

        p = self.get_authkey_file(ak['authfile'], ak['user'])
        if p is None:
            perror("could not determine", ak['authfile'], "location")
            return RET_ERR
        base = os.path.dirname(p)

        if not os.path.exists(base):
            os.makedirs(base, 0o0700)
            pinfo(base, "created")
            if p.startswith(os.path.expanduser('~'+ak['user'])):
                os.chown(base, userinfo.pw_uid, userinfo.pw_gid)
                pinfo(base, "ownership set to %d:%d"%(userinfo.pw_uid, userinfo.pw_gid))

        if not os.path.exists(p):
            with open(p, 'w') as f:
                f.write("")
                pinfo(p, "created")
                os.chmod(p, 0o0600)
                pinfo(p, "mode set to 0600")
                os.chown(p, userinfo.pw_uid, userinfo.pw_gid)
                pinfo(p, "ownetship set to %d:%d"%(userinfo.pw_uid, userinfo.pw_gid))

        with open(p, 'a') as f:
            f.write(ak['key'])
            if not ak['key'].endswith('\n'):
                f.write('\n')
            pinfo('key', self.truncate_key(ak['key']), 'installed for user', ak['user'])

        return RET_OK

    def del_authkey(self, ak):
        if self.check_authkey(ak, verbose=False) == RET_OK:
            pinfo('key', self.truncate_key(ak['key']), 'is already not installed for user', ak['user'])
            return RET_OK

        ps = self.get_authkey_files(ak['user'])

        for p in ps:
            base = os.path.basename(p)
            if not os.path.exists(p):
                continue

            with open(p, 'r') as f:
                l = f.read().split('\n')

            n = len(l)
            while True:
                try:
                    l.remove(ak['key'].replace('\n', ''))
                except ValueError:
                    break
            if len(l) == n:
                # nothing changed
                continue

            with open(p, 'w') as f:
                f.write('\n'.join(l))
                pinfo('key', self.truncate_key(ak['key']), 'uninstalled for user', ak['user'])

        return RET_OK

    def check(self):
        r = 0
        for ak in self.authkeys:
            r |= self.check_authkey(ak)
            if ak['action'] == 'add':
                r |= self.check_allowgroup(ak)
                r |= self.check_allowuser(ak)
        return r

    def fix(self):
        r = 0
        for ak in self.authkeys:
            r |= self.fix_authkey(ak)
            if ak['action'] == 'add':
                r |= self.fix_allowgroups(ak)
                r |= self.fix_allowusers(ak)
        return r

if __name__ == "__main__":
    main(CompAuthKeys)

