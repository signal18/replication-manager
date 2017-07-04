#!/usr/bin/env python

data = {
  "default_prefix": "OSVC_COMP_REMOVE_FILES_",
  "example_value": """
[
  "/tmp/foo",
  "/bar/to/delete"
]
""",
  "description": """* Verify files and file trees are uninstalled
""",
  "form_definition": """
Desc: |
  A rule defining a set of files to remove, fed to the 'remove_files' compliance object.
Css: comp48

Outputs:
  -
    Dest: compliance variable
    Class: remove_files
    Type: json
    Format: list

Inputs:
  -
    Id: path
    Label: File path
    DisplayModeLabel: ""
    LabelCss: edit16
    Mandatory: Yes
    Help: You must set paths in fully qualified form.
    Type: string
""",
}

import os
import sys
import re
import json
from glob import glob
import shutil

sys.path.append(os.path.dirname(__file__))

from comp import *

blacklist = [
  "/",
  "/root"
]

class CompRemoveFiles(CompObject):
    def __init__(self, prefix=None):
        CompObject.__init__(self, prefix=prefix, data=data)

    def init(self):
        patterns = self.get_rules()

        patterns = sorted(list(set(patterns)))
        self.files = self.expand_patterns(patterns)

        if len(self.files) == 0:
            pinfo("no files matching patterns")
            raise NotApplicable

    def expand_patterns(self, patterns):
        l = []
        for pattern in patterns:
            l += glob(pattern)
        return l

    def fixable(self):
        return RET_NA

    def check_file(self, _file):
        if not os.path.exists(_file):
            pinfo(_file, "does not exist. on target.")
            return RET_OK
        perror(_file, "exists. shouldn't")
        return RET_ERR

    def fix_file(self, _file):
        if not os.path.exists(_file):
            return RET_OK
        try:
            if os.path.isdir(_file) and not os.path.islink(_file):
                shutil.rmtree(_file)
            else:
                os.unlink(_file)
            pinfo(_file, "deleted")
        except Exception as e:
            perror("failed to delete", _file, "(%s)"%str(e))
            return RET_ERR
        return RET_OK

    def check(self):
        r = 0
        for _file in self.files:
            r |= self.check_file(_file)
        return r

    def fix(self):
        r = 0
        for _file in self.files:
            r |= self.fix_file(_file)
        return r

if __name__ == "__main__":
    main(CompRemoveFiles)
