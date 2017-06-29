#!/usr/bin/env python

data = {
  "default_prefix": "OSVC_COMP_FILEINCCOM_",
  "description": """Verify file content.
Alert if a pattern is present but never fix.
The variable format is json-serialized.
""",
  "example_value": """
{
  "path": "/some/path/to/file",
  "check": "some pattern into the file",
  "com": "comment to correct manualy"
}
""",
  "form_definition": """
Desc: |
  A fileinccom rule, fed to the 'fileinc' compliance object to verify a line matching the 'check' regular expression is present in the specified file.
Css: comp48

Outputs:
  -
    Dest: compliance variable
    Class: fileinccom
    Type: json
    Format: dict

Inputs:
  -
    Id: path
    Label: Path
    DisplayModeLabel: path
    LabelCss: hd16
    Mandatory: Yes
    Help: File path to search the matching line into.
    Type: string

  -
    Id: check
    Label: Check regexp
    DisplayModeLabel: check
    LabelCss: action16
    Mandatory: Yes
    Help: A regular expression. Matching the regular expression is sufficent to grant compliancy.
    Type: string

  -
    Id: com
    Label: Comment
    DisplayModeLabel: com
    LabelCss: action16
    Help: Give indications to fix manualy.
    Type: string
""",
}

import os
import sys
import stat
import re
import urllib
import tempfile
import codecs

sys.path.append(os.path.dirname(__file__))

from comp import *

MAXSZ = 8*1024*1024

class FileIncCom(CompObject):
    def __init__(self, prefix=None):
        CompObject.__init__(self, prefix=prefix, data=data)

    def init(self):
        self.files = {}
        self.ok = {}
        self.checks = []
        self.upds = {}

        self.sysname, self.nodename, x, x, self.machine = os.uname()

        for rule in self.get_rules():
            try:
                self.add_file(rule)
            except ValueError:
                perror('syntax error in rule', rule)

    def fixable(self):
        return RET_NA

    def fix(self):
        return RET_NA

    def read_file(self, path):
        if not os.path.exists(path):
            return ''
        out = ''
        try :
            f = codecs.open(path, 'r', encoding="utf8", errors="ignore")
            out = f.read()
            f.close()
        except IOError as e:
            pinfo("cannot read '%s', error=%d - %s" %(path, e.errno, str(e)))
            raise
        except:
            perror("Cannot open '%s', unexpected error: %s"%(path, sys.exc_info()[0]))
            raise
        return out

    def add_file(self,d):
        r = RET_OK
        if 'path' not in d:
            perror("'path' should be defined:", d)
            r |= RET_ERR
        if 'path' in d:
            d['path'] = d['path'].strip()
        if not d['path'] in self.upds:
            self.upds[d['path']] = 0
        if not d['path'] in self.files:
            try:
                fsz = os.path.getsize(d['path'])
            except:
                fsz = 0
            if fsz > MAXSZ:
                self.ok[d['path']] = 0
                self.files[d['path']] = ''
                perror("file '%s' is too large [%.2f Mb] to fit" %(d['path'], fsz/(1024.*1024)))
                r |= RET_ERR
            else:
                try:
                    self.files[d['path']] = self.read_file(d['path'])
                    self.ok[d['path']] = 1
                except:
                    self.files[d['path']] = ""
                    self.ok[d['path']] = 0
                    r |= RET_ERR
        val = True
        self.checks.append({'check':d['check'], 'path':d['path'], 'valid':val, 'com':d['com']})
        return r
        
    def check(self):
        r = RET_OK
        for ck in self.checks:
            if not ck['valid']:
                perror("rule error: '%s' does not match target content" % ck['check'])
                r |= RET_ERR
                continue
            if self.ok[ck['path']] != 1:
                r |= RET_ERR
                continue
            pr = RET_OK
            m = 0
            ok = 0
            lines = self.files[ck['path']].split('\n')
            for line in lines:
                if re.match(ck['check'], line):
                    pinfo("line '%s' found in '%s'" %(line, ck['path']))
                    ok += 1
            if ok == 0:
                perror("pattern '%s' not found in %s. To correct: %s"%(ck['check'], ck['path'], ck['com']))
                pr |= RET_ERR
            r |= pr
        return r



if __name__ == "__main__":
    main(FileIncCom)
