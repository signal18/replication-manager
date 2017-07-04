#!/usr/bin/env python

data = {
  "default_prefix": "OSVC_COMP_FILEINC_",
  "example_value": """ 
{
 "path": "/tmp/foo",
 "check": ".*some pattern.*",
 "fmt": "full added content with %%HOSTNAME%%@corp.com: some pattern into the file."
}
  """,
  "description": """* Verify file content.
* The collector provides the format with wildcards.
* The module replace the wildcards with contextual values.
* The fmt must match the check pattern

Wildcards:
%%ENV:VARNAME%%		Any environment variable value
%%HOSTNAME%%		Hostname
%%SHORT_HOSTNAME%%	Short hostname

""",
  "form_definition": """
Desc: |
  A fileinc rule, fed to the 'fileinc' compliance object to verify a line matching the 'check' regular expression is present in the specified file.
Css: comp48

Outputs:
  -
    Dest: compliance variable
    Class: fileinc
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
    Id: fmt
    Label: Format
    DisplayModeLabel: fmt
    LabelCss: action16
    Help: The line installed if the check pattern is not found in the file.
    Type: string
  -
    Id: ref
    Label: URL to format
    DisplayModeLabel: ref
    LabelCss: loc
    Help: An URL pointing to a file containing the line installed if the check pattern is not found in the file.
    Type: string

""",
}

import os
import sys
import json
import stat
import re
import urllib
import tempfile
import codecs

sys.path.append(os.path.dirname(__file__))

from comp import *

MAXSZ = 8*1024*1024

class CompFileInc(CompObject):
    def __init__(self, prefix=None):
        CompObject.__init__(self, prefix=prefix, data=data)

    def init(self):
        self.files = {}
        self.ok = {}
        self.checks = []
        self.upds = {}

        self.sysname, self.nodename, x, x, self.machine = os.uname()
        for rule in self.get_rules():
            self.add_rule(rule)

        if len(self.checks) == 0:
            raise NotApplicable()

    def fixable(self):
        return RET_NA

    def parse_fmt(self, x):
        if isinstance(x, int):
            x = str(x)
        x = x.replace('%%HOSTNAME%%', self.nodename)
        x = x.replace('%%SHORT_HOSTNAME%%', self.nodename.split('.')[0])
        return x

    def parse_ref(self, url):
        f = tempfile.NamedTemporaryFile()
        tmpf = f.name
        try:
            self.urlretrieve(url, tmpf)
            f.close()
        except Exception as e:
             perror(url, "download error:", e)
             return ''
        content = unicode(f.read())
        return self.parse_fmt(content)

    def read_file(self, path):
        if not os.path.exists(path):
            return ''
        out = ''
        try :
            f = codecs.open(path, 'r', encoding="utf8", errors="ignore")
            out = f.read().rstrip('\n')
            f.close()
        except IOError as e:
            pinfo("cannot read '%s', error=%d - %s" %(path, e.errno, str(e)))
            raise
        except:
            perror("Cannot open '%s', unexpected error: %s"%(path, sys.exc_info()[0]))
            raise
        return out

    def add_rule(self, d):
        r = RET_OK
        if 'path' not in d:
            perror("'path' should be defined:", d)
            r |= RET_ERR
        if 'fmt' in d and 'ref' in d:
            perror("'fmt' and 'ref' are exclusive:", d)
            r |= RET_ERR
        if 'path' in d:
            d['path'] = d['path'].strip()
        if 'ref' in d:
            d['ref'] = d['ref'].strip()
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
        c = ''
        if 'fmt' in d:
            c = self.parse_fmt(d['fmt'])
        elif 'ref' in d:
            c = self.parse_ref(d['ref'])
        else:
            perror("'fmt' or 'ref' should be defined:", d)
            r |= RET_ERR
        c = c.strip()
        if re.match(d['check'], c) is not None or len(c) == 0:
            val = True
        else:
            val = False
            r |= RET_ERR
        self.checks.append({'check':d['check'], 'path':d['path'], 'add':c, 'valid':val})
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
                    m += 1
                    if len(ck['add']) > 0 and line == ck['add']:
                        pinfo("line '%s' found in '%s'" %(line, ck['path']))
                        ok += 1
                    if m > 1:
                        perror("duplicate match of pattern '%s' in '%s'"%(ck['check'], ck['path']))
                        pr |= RET_ERR
            if len(ck['add']) == 0:
                if m > 0:
                    perror("pattern '%s' found in %s"%(ck['check'], ck['path']))
                    pr |= RET_ERR
                else:
                    pinfo("pattern '%s' not found in %s"%(ck['check'], ck['path']))
            elif ok == 0:
                perror("line '%s' not found in %s"%(ck['add'], ck['path']))
                pr |= RET_ERR
            elif m == 0:
                perror("pattern '%s' not found in %s"%(ck['check'], ck['path']))
                pr |= RET_ERR
            r |= pr
        return r

    def rewrite_files(self):
        r = RET_OK
        for path in self.files:
            if self.upds[path] == 0:
                continue
            if self.ok[path] != 1:
                r |= RET_ERR
                continue
            if not os.path.exists(path):
                perror("'%s' will be created, please check owner and permissions" %path)
            try:
                f = codecs.open(path, 'w', encoding="utf8")
                f.write(self.files[path])
                f.close()
                pinfo("'%s' successfully rewritten" %path)
            except:
                perror("failed to rewrite '%s'" %path)
                r |= RET_ERR
        return r

    def fix(self):
        r = RET_OK
        for ck in self.checks:
            if not ck['valid']:
                perror("rule error: '%s' does not match target content" % ck['check'])
                r |= RET_ERR
                continue
            if self.ok[ck['path']] != 1:
                r |= RET_ERR
                continue
            need_rewrite = False
            m = 0
            lines = self.files[ck['path']].rstrip('\n').split('\n')
            for i, line in enumerate(lines):
                if re.match(ck['check'], line):
                    m += 1
                    if m == 1:
                        if line != ck['add']:
                            # rewrite line
                            pinfo("rewrite %s:%d:'%s', new content: '%s'" %(ck['path'], i, line, ck['add']))
                            lines[i] = ck['add']
                            need_rewrite = True
                    elif m > 1:
                        # purge dup
                        pinfo("remove duplicate line %s:%d:'%s'" %(ck['path'], i, line))
                        lines[i] = ""
                        need_rewrite = True
            if m == 0 and len(ck['add']) > 0:
                pinfo("add line '%s' to %s"%(ck['add'], ck['path']))
                lines.append(ck['add'])
                need_rewrite = True

            if need_rewrite:
                self.files[ck['path']] = '\n'.join(lines).rstrip("\n")+"\n"
                self.upds[ck['path']] = 1

        r |= self.rewrite_files()
        return r


if __name__ == "__main__":
    main(CompFileInc)
