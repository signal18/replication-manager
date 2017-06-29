#!/usr/bin/env python

data = {
  "default_prefix": "OSVC_COMP_FILEPROP_",
  "example_value": """ 
{
  "path": "/some/path/to/file",
  "mode": "750",
  "uid": 500,
  "gid": 500,
}
  """,
  "description": """* Verify file existance, mode and ownership.
* The collector provides the format with wildcards.
* The module replace the wildcards with contextual values.

In fix() the file is created empty with the right mode & ownership.
""",
  "form_definition": """
Desc: |
  A fileprop rule, fed to the 'fileprop' compliance object to verify the target file ownership and permissions.
Css: comp48
Outputs:
  -
    Dest: compliance variable
    Class: fileprop
    Type: json
    Format: dict
Inputs:
  -
    Id: path
    Label: Path
    DisplayModeLabel: path
    LabelCss: action16
    Mandatory: Yes
    Help: File path to check the ownership and permissions for.
    Type: string
  -
    Id: mode
    Label: Permissions
    DisplayModeLabel: perm
    LabelCss: action16
    Help: "In octal form. Example: 644"
    Type: integer
  -
    Id: uid
    Label: Owner
    DisplayModeLabel: uid
    LabelCss: guy16
    Help: Either a user ID or a user name
    Type: string or integer
  -
    Id: gid
    Label: Owner group
    DisplayModeLabel: gid
    LabelCss: guy16
    Help: Either a group ID or a group name
    Type: string or integer
""",
}

import os
import sys
import json
import stat
import re
import pwd
import grp

sys.path.append(os.path.dirname(__file__))

from comp import *

class CompFileProp(CompObject):
    def __init__(self, prefix=None):
        CompObject.__init__(self, prefix=prefix, data=data)

    def init(self):
        self._usr = {}
        self._grp = {}
        self.sysname, self.nodename, x, x, self.machine = os.uname()
        self.files = []

        for rule in self.get_rules():
            try:
                self.files += self.add_file(rule)
            except InitError:
                continue
            except ValueError:
                perror('fileprop: failed to parse variable', os.environ[k])

        if len(self.files) == 0:
            raise NotApplicable()

    def add_file(self, d):
        if 'path' not in d:
            perror('fileprop: path should be in the dict:', d)
            RET = RET_ERR
            return []
        try:
            d["uid"] = int(d["uid"])
        except:
            pass
        try:
            d["gid"] = int(d["gid"])
        except:
            pass
        return [d]

    def fixable(self):
        return RET_NA

    def check_file_type(self, f, verbose=False):
        r = RET_OK
        if not os.path.exists(f["path"].rstrip("/")):
            if verbose: perror("fileprop:", f["path"], "does not exist")
            r = RET_ERR
        elif f["path"].endswith("/") and not os.path.isdir(f["path"]):
            if verbose: perror("fileprop:", f["path"], "exists but is not a directory")
            r = RET_ERR
        elif not f["path"].endswith("/") and os.path.isdir(f["path"]):
            if verbose: perror("fileprop:", f["path"], "exists but is a directory")
            r = RET_ERR
        return r  

    def check_file_mode(self, f, verbose=False):
        if 'mode' not in f:
            return RET_OK
        try:
            mode = oct(stat.S_IMODE(os.stat(f['path']).st_mode))
        except:
            if verbose: perror("fileprop:", f['path'], 'can not stat file')
            return RET_ERR
        mode = str(mode).lstrip("0")
        if mode != str(f['mode']):
            if verbose: perror("fileprop:", f['path'], 'mode should be %s but is %s'%(f['mode'], mode))
            return RET_ERR
        return RET_OK

    def get_uid(self, uid):
        if uid in self._usr:
            return self._usr[uid]
        tuid = uid
        if isinstance(uid, (str, unicode)):
            try:
                info=pwd.getpwnam(uid)
                tuid = info[2]
                self._usr[uid] = tuid
            except:
                perror("fileprop:", "user %s does not exist"%uid)
                raise ComplianceError()
        return tuid

    def get_gid(self, gid):
        if gid in self._grp:
            return self._grp[gid]
        tgid = gid
        if isinstance(gid, (str, unicode)):
            try:
                info=grp.getgrnam(gid)
                tgid = info[2]
                self._grp[gid] = tgid
            except:
                perror("fileprop:",  "group %s does not exist"%gid)
                raise ComplianceError()
        return tgid

    def check_file_uid(self, f, verbose=False):
        if 'uid' not in f:
            return RET_OK
        tuid = self.get_uid(f['uid'])
        try:
            uid = os.stat(f['path']).st_uid
        except:
            if verbose: perror("fileprop:", f['path'], 'can not stat file')
            return RET_ERR
        if uid != tuid:
            if verbose: perror("fileprop:", f['path'], 'uid should be %s but is %s'%(tuid, str(uid)))
            return RET_ERR
        return RET_OK

    def check_file_gid(self, f, verbose=False):
        if 'gid' not in f:
            return RET_OK
        tgid = self.get_gid(f['gid'])
        try:
            gid = os.stat(f['path']).st_gid
        except:
            if verbose: perror("fileprop:", f['path'], 'can not stat file')
            return RET_ERR
        if gid != tgid:
            if verbose: perror("fileprop:", f['path'], 'gid should be %s but is %s'%(tgid, str(gid)))
            return RET_ERR
        return RET_OK

    def check_file_exists(self, f):
        if not os.path.exists(f['path']):
            return RET_ERR
        return RET_OK

    def check_file(self, f, verbose=False):
        if self.check_file_type(f, verbose) == RET_ERR:
            return RET_ERR
        r = 0
        r |= self.check_file_mode(f, verbose)
        r |= self.check_file_uid(f, verbose)
        r |= self.check_file_gid(f, verbose)
        if r == 0 and verbose:
            pinfo("fileprop:", f['path'], "is ok")
        return r

    def fix_file_mode(self, f):
        if 'mode' not in f:
            return RET_OK
        if self.check_file_mode(f) == RET_OK:
            return RET_OK
        try:
            pinfo("fileprop:", "%s mode set to %s"%(f['path'], str(f['mode'])))
            os.chmod(f['path'], int(str(f['mode']), 8))
        except:
            return RET_ERR
        return RET_OK

    def fix_file_owner(self, f):
        uid = -1
        gid = -1

        if 'uid' not in f and 'gid' not in f:
            return RET_OK
        if 'uid' in f and self.check_file_uid(f) != RET_OK:
            uid = self.get_uid(f['uid'])
        if 'gid' in f and self.check_file_gid(f) != RET_OK:
            gid = self.get_gid(f['gid'])
        if uid == -1 and gid == -1:
            return RET_OK
        try:
            os.chown(f['path'], uid, gid)
        except:
            perror("fileprop:", "failed to set %s ownership to %d:%d"%(f['path'], uid, gid))
            return RET_ERR
        pinfo("fileprop:", "%s ownership set to %d:%d"%(f['path'], uid, gid))
        return RET_OK

    def fix_file_notexists(self, f):
        if not os.path.exists(f['path'].rstrip("/")):
            if f['path'].endswith("/"):
                try:
                    os.makedirs(f['path'])
                    pinfo("fileprop:", f['path'], "created")
                except:
                    perror("fileprop:", "failed to create", f['path'])
                    return RET_ERR
                return RET_OK
            else:
                dirname = os.path.dirname(f['path'])
                if not os.path.exists(dirname):
                    pinfo("fileprop:", "create", dirname)
                    try:
                        os.makedirs(dirname)
                    except Exception as e:
                        perror("fileprop:", "failed to create", dirname)
                        return RET_ERR
                pinfo("fileprop:", "touch", f['path'])
                open(f['path'], 'a').close()
        elif f['path'].endswith("/") and not os.path.isdir(f['path']):
                pinfo("fileprop:", "delete file", f['path'].rstrip("/"))
                try:
                    os.unlink(f['path'].rstrip("/"))
                except Exception as e:
                    perror("fileprop:", e)
                    return RET_ERR
                pinfo("fileprop:", "make directory", f['path'])
                try:
                    os.makedirs(f['path'])
                except Exception as e:
                    perror("fileprop:", e)
                    return RET_ERR
        elif not f['path'].endswith("/") and os.path.isdir(f['path']):
            perror("fileprop:", "cowardly refusing to remove the existing", f['path'], "directory to create a regular file")
            return RET_ERR

        if self.check_file_exists(f) == RET_OK:
            return RET_OK
        d = os.path.dirname(f['path'])
        if not os.path.exists(d):
           os.makedirs(d)
           try:
               os.chown(d, f['uid'], f['gid'])
           except:
               pass
        try:
            with open(f['path'], 'w') as fi:
                fi.write('')
        except:
            return RET_ERR
        pinfo("fileprop:", f['path'], "created")
        return RET_OK

    def check(self):
        r = 0
        for f in self.files:
            r |= self.check_file(f, verbose=True)
        return r

    def fix(self):
        r = 0
        for f in self.files:
            r |= self.fix_file_notexists(f)
            r |= self.fix_file_mode(f)
            r |= self.fix_file_owner(f)
        return r


if __name__ == "__main__":
    main(CompFileProp)

