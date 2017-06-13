#!/usr/bin/env python

data = {
  "default_prefix": "OSVC_COMP_FILE_",
  "example_value": """ 
{
  "symlink": "/tmp/foo",
  "target": "/tmp/bar"
}
""",
  "description": """* Verify symlink's existance.
* The collector provides the format with wildcards.
* The module replace the wildcards with contextual values.
* In the 'fix' the symlink is created (and intermediate dirs if required).
* There is no check or fix for target's existance.
* There is no check or fix for mode or ownership of either symlink or target.
""",
  "form_definition": """
Desc: |
  A symfile rule, fed to the 'symlink' compliance object to create a Unix symbolic link.
Css: comp48

Outputs:
  -
    Dest: compliance variable
    Class: symlink
    Type: json
    Format: dict

Inputs:
  -
    Id: symlink
    Label: Symlink path
    DisplayModeLabel: symlink
    LabelCss: hd16
    Mandatory: Yes
    Help: The full path of the symbolic link to check or create.
    Type: string

  -
    Id: target
    Label: Target path
    DisplayModeLabel: target
    LabelCss: hd16
    Mandatory: Yes
    Help: The full path of the target file pointed by the symlink.
    Type: string
"""
}

import os
import errno
import sys
import stat
import re
import pwd
import grp

sys.path.append(os.path.dirname(__file__))

from comp import *

class InitError(Exception):
    pass

class CompSymlink(CompObject):
    def __init__(self, prefix='OSVC_COMP_SYMLINK_'):
        CompObject.__init__(self, prefix=prefix, data=data)

    def init(self):
        self.sysname, self.nodename, x, x, self.machine = os.uname()
        self.symlinks = []

        for rule in self.get_rules():
            try:
                self.symlinks += self.add_symlink(rule)
            except InitError:
                continue
            except ValueError:
                perror('symlink: failed to parse variable', rule)

    def add_symlink(self, v):
        if 'symlink' not in v:
            perror('symlink should be in the dict:', d)
            RET = RET_ERR
            return []
        if 'target' not in v:
            perror('target should be in the dict:', d)
            RET = RET_ERR
            return []
        return [v]

    def fixable(self):
        return RET_NA

    def check_symlink_exists(self, f):
        if not os.path.islink(f['symlink']):
            return RET_ERR
        return RET_OK

    def check_symlink(self, f, verbose=False):
        if not os.path.islink(f['symlink']):
            perror("symlink", f['symlink'], "does not exist")
            return RET_ERR
        if os.readlink(f['symlink']) != f['target']:
            perror("symlink", f['symlink'], "does not point to", f['target'])
            return RET_ERR
        if verbose:
            pinfo("symlink", f['symlink'], "->", f['target'], "is ok")
        return RET_OK

    def fix_symlink_notexists(self, f):
        if self.check_symlink_exists(f) == RET_OK:
            return RET_OK
        d = os.path.dirname(f['symlink'])
        if not os.path.exists(d):
           try:
               os.makedirs(d)
           except OSError as e:
               if e.errno == 20:
                   perror("symlink: can not create dir", d, "to host the symlink", f['symlink'], ": a parent is not a directory")
                   return RET_ERR
               raise
        try:
           os.symlink(f['target'], f['symlink'])
        except:
            return RET_ERR
        pinfo("symlink", f['symlink'], "->", f['target'], "created")
        return RET_OK

    def check(self):
        r = 0
        for f in self.symlinks:
            r |= self.check_symlink(f, verbose=True)
        return r

    def fix(self):
        r = 0
        for f in self.symlinks:
            r |= self.fix_symlink_notexists(f)
        return r

if __name__ == "__main__":
    main(CompSymlink)

