#!/usr/bin/env python

data = {
  "default_prefix": "OSVC_COMP_ANSIBLE_PLAYBOOK_",
  "example_value": """ 
{
  "path": "/some/path/to/file",
  "fmt": "---",
}
  """,
  "description": """* Fetch a playbook from a href if required
* Run the playbook in check mode on check action
* Run the playbook on fix action
""",
  "form_definition": """
Desc: |
  Define or point to a ansible playbook.
Css: comp48

Outputs:
  -
    Dest: compliance variable
    Class: file
    Type: json
    Format: dict

Inputs:
  -
    Id: ref
    Label: Content URL pointer
    DisplayModeLabel: ref
    LabelCss: loc
    Help: "Examples:
        /path/to/reference_file
        http://server/path/to/reference_file
        https://server/path/to/reference_file
        ftp://server/path/to/reference_file
        ftp://login:pass@server/path/to/reference_file"
    Type: string
  -
    Id: fmt
    Label: Content
    DisplayModeLabel: fmt
    LabelCss: hd16
    Css: pre
    Help: A reference content for the file. The text can embed substitution variables specified with %%ENV:VAR%%.
    Type: text
"""
}

import os
import sys
import stat
import re
import tempfile
from subprocess import *

sys.path.append(os.path.dirname(__file__))

from comp import *

class InitError(Exception):
    pass

class AnsiblePlaybook(CompObject):
    def __init__(self, prefix=None):
        CompObject.__init__(self, prefix=prefix, data=data)

    def init(self):
        self.rules = []
        self.inventory = os.path.join(os.environ["OSVC_PATH_COMP"], ".ansible-inventory")

        for rule in self.get_rules():
            try:
                self.rules += self.add_rule(rule)
            except InitError:
                continue
            except ValueError:
                perror('ansible_playbook: failed to parse variable', os.environ[k])

        if len(self.rules) == 0:
            raise NotApplicable()

    def add_rule(self, d):
        if 'fmt' not in d and 'ref' not in d:
            perror('file: fmt or ref should be in the dict:', d)
            RET = RET_ERR
            return []
        if 'fmt' in d and 'ref' in d:
            perror('file: fmt and ref are exclusive:', d)
            RET = RET_ERR
            return []
        return [d]

    def download(self, d):
        if 'ref' in d and d['ref'].startswith("safe://"):
            return self.get_safe_file(d["ref"])
        elif 'fmt' in d and d['fmt'] != "":
            return self.write_fmt(d)
        else:
            return self.download_url()

    def download_url(self, d):
        f = tempfile.NamedTemporaryFile()
        tmpf = f.name
        f.close()
        try:
            self.urlretrieve(d['ref'], tmpf)
        except IOError as e:
            perror("file ref", d['ref'], "download failed:", e)
            raise InitError()
        return tmpf

    def get_safe_file(self, uuid):
        tmpf = tempfile.NamedTemporaryFile()
        tmpfname = tmpf.name
        tmpf.close()
        try:
            self.collector_safe_file_download(uuid, tmpfname)
        except Exception as e:
            raise ComplianceError("%s: %s" % (uuid, str(e)))
        return tmpfname

    def write_fmt(self, f):
        tmpf = tempfile.NamedTemporaryFile()
        tmpfname = tmpf.name
        tmpf.close()
        with open(tmpfname, 'w') as tmpf:
            tmpf.write(f['fmt'])
        return tmpfname

    def write_inventory(self):
        if os.path.exists(self.inventory):
            return
        with open(self.inventory, 'w') as ofile:
            ofile.write("[local]\n127.0.0.1\n")

    def fixable(self):
        return RET_NA

    def fix_playbook(self, rule, verbose=False):
        tmpfname = self.download(rule)
        try:
            return self._fix_playbook(rule, tmpfname, verbose=verbose)
        finally:
            os.unlink(tmpfname)

    def _fix_playbook(self, rule, tmpfname, verbose=False):
        self.write_inventory()
        cmd = ["ansible-playbook", "-c", "local", "-i", self.inventory, tmpfname]
        proc = Popen(cmd, stdout=PIPE, stderr=PIPE)
        out, err = proc.communicate()
        pinfo(out)
        perror(err)
        if proc.returncode != 0:
            return RET_ERR
        if "failed=0" in out:
            return RET_OK
        return RET_ERR

    def check_playbook(self, rule, verbose=False):
        tmpfname = self.download(rule)
        try:
            return self._check_playbook(rule, tmpfname, verbose=verbose)
        finally:
            os.unlink(tmpfname)

    def _check_playbook(self, rule, tmpfname, verbose=False):
        self.write_inventory()
        cmd = ["ansible-playbook", "-c", "local", "-i", self.inventory, "--check", tmpfname]
        proc = Popen(cmd, stdout=PIPE, stderr=PIPE)
        out, err = proc.communicate()
        pinfo(out)
        perror(err)
        if proc.returncode != 0:
            return RET_ERR
        if "changed=0" in out and "failed=0" in out:
            return RET_OK
        return RET_ERR

    def check(self):
        r = 0
        for rule in self.rules:
            r |= self.check_playbook(rule, verbose=True)
        return r

    def fix(self):
        r = 0
        for rule in self.rules:
            r |= self.fix_playbook(rule)
        return r

if __name__ == "__main__":
    main(AnsiblePlaybook)

