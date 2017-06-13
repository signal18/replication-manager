#!/usr/bin/env python

data = {
  "default_prefix": "OSVC_COMP_CRONTABENTRY_",
  "description": """* Verify crontab content. Fix if appropriate.
* The collector provides the format with wildcards.
* The module replace the wildcards with contextual values.
""",
  "example_value": """
{
  "user": "opensvc",
  "check": "/path/to/mycron",
  "entry": "3,13,23,33,43,53 * * * *  /path/to/mycron >/dev/null 2>&1"
}
""",
  "form_definition": """
Desc: |
  A cron rule, defining a Unix crontab entry, fed to the 'cron' compliance object.
Css: comp48

Outputs:
  -
    Dest: compliance variable
    Class: cron
    DisplayClass: raw
    Template: "%%ACTION%%:%%USER%%:%%SCHEDULE%%:%%COMMAND%%:%%FILE%%"

Inputs:
  -
    Id: ACTION
    Label: Action
    LabelCss: action16
    Mandatory: Yes
    Candidates:
       - add
       - del
    Help: Define if the crontab entry must be installed or not installed.
    Type: string

  -
    Id: USER
    Label: User name
    LabelCss: guy16
    Mandatory: Yes
    Help: Which Unix user should this entry be installed or uninstalled for.
    Type: string

  -
    Id: SCHEDULE
    Label: Schedule
    LabelCss: time16
    Mandatory: Yes
    Help: "The Unix cron format schedule : minute hour day_of_month month day_of_week."
    Type: string

  -
    Id: COMMAND
    Label: Command
    LabelCss: action16
    Mandatory: Yes
    Help: The command to schedule.
    Type: string

  -
    Id: FILE
    Label: Cron file name
    LabelCss: action16
    Help: Some Unix systems support split-file crontabs. For those, you can specify here the filename you want to entry to be added to. For systems without split-file crontabs, the crontab file is based on the user name specified above.
    Type: string
"""
}

import os
import sys
import stat
import re
import urllib
import tempfile
import pwd
import grp
from subprocess import *

sys.path.append(os.path.dirname(__file__))

from comp import *

class CrontabEntry(CompObject):
    def __init__(self, prefix=None):
        CompObject.__init__(self, prefix=prefix, data=data)

    def init(self):
        self.crontabs = {}
        self.checks = []
        self.upds = {}

        self.sysname, self.nodename, x, x, self.machine = os.uname()

        rules = self.get_rules()
        for rule in rules:
            try:
                self.add_crontab(rule)
            except ValueError:
                perror('syntax error in rule ', rule)

        if len(self.checks) == 0:
            raise NotApplicable()

    def fixable(self):
        return RET_OK

    def parse_entry(self, x):
        if isinstance(x, int):
            x = str(x)+'\n'
        x = x.replace('%%HOSTNAME%%', self.nodename)
        x = x.replace('%%SHORT_HOSTNAME%%', self.nodename.split('.')[0])
        if not x.endswith('\n'):
            x += '\n'
        return x

    def parse_ref(self, url):
        f = tempfile.NamedTemporaryFile()
        tmpf = f.name
        try:
            fname, headers = urllib.urlretrieve(url, tmpf)
            if 'invalid file' in headers.values():
                perror(url, "not found on collector")
                return RET_ERR
            content = unicode(f.read())
            f.close()
        except:
             perror(url, "not found on collector")
             return ''
        if '<title>404 Not Found</title>' in content:
            perror(url, "not found on collector")
            return ''
        return self.parse_entry(content)

    def read_crontab(self, user):
        if user == '':
            user = 'root'
        if self.sysname == "Linux":
            cmd = ['/usr/bin/crontab', '-u', user, '-l']
        else:
            cmd = ['/usr/bin/crontab', '-l', user]
        p = Popen(cmd, stdout=PIPE, stderr=PIPE)
        out,err = p.communicate()
        if p.returncode != 0 :
            err = bdecode(err)
            perror("Cannot get %s's %s" %(user,err.strip('\n')))
            return RET_ERR, ''
        return RET_OK, bdecode(out)

    def add_crontab(self, d):
        r = RET_OK
        if 'user' not in d:
            perror('user should be defined:', d)
            r |= RET_ERR
        if 'entry' in d and 'ref' in d:
            perror('entry and ref are exclusive:', d)
            r |= RET_ERR
        if len(d['user']) < 1:
            d['user'] = 'root'
        if not d['user'] in self.upds:
            self.upds[d['user']] = 0
        if not d['user'] in self.crontabs:
            rx, text = self.read_crontab(d['user'])
            if rx == RET_OK:
                self.crontabs[d['user']] = text
            else:
                self.crontabs[d['user']] = ''
        c = ''
        if 'entry' in d:
            c = self.parse_entry(d['entry'])
        elif 'ref' in d:
            c = self.parse_ref(d['ref'])
        else:
            perror('entry or ref should be defined:', d)
            r |= RET_ERR
        if d['check'] in c:
            val = True
        else:
            val = False
            r |= RET_ERR
        self.checks.append({'user':d['user'], 'check':d['check'], 'add':c, 'valid':val})
        return r
        
    def check_crontab(self):
        r = RET_OK
        for ck in self.checks:
             if not ck['valid']:
                 perror("Pattern '%s' for %s's crontab is absent in the requested content" %(ck['check'], ck['user']))
                 r |= RET_ERR
                 continue
             pr = RET_ERR
             lines = self.crontabs[ck['user']].split('\n')
             for line in lines:
                 if line.startswith('#'):
                     continue
                 if ck['check'] in line:
                     pr = RET_OK
                     break
             if pr == RET_OK:
                 pinfo("Pattern '%s' matches %s's crontab, entry: %s => OK" %(ck['check'], ck['user'], '"'+line+'"'))
             else:
                 perror("Pattern '%s' **does not match** %s's crontab => BAD" %(ck['check'], ck['user']))
             r |= pr
        return r

    def check(self):
        r = 0
        r |= self.check_crontab()
        return r

    def add_2_crontabs(self):
        r = RET_OK
        f = tempfile.NamedTemporaryFile()
        filen = f.name
        f.close()
        for user in self.crontabs:
            if self.upds[user] == 0:
                continue
            f = open(filen, 'w+')
            try:
                f.writelines(self.crontabs[user])
                f.close()
            except:
                perror("fail to close temporary file %s" %filen)
                r |= RET_ERR
            if user != 'root':
                cmd = ['/usr/bin/su', user, '-c', '/usr/bin/crontab '+filen]
            else:
                cmd = ['/usr/bin/crontab', filen]
            p = Popen(cmd, stdout=PIPE, stderr=PIPE)
            out, err = p.communicate()
            if p.returncode != 0:
                err = bdecode(err)
                perror("Could not append-to or create %s's %s" %(user,err.strip('\n')))
                r |= RET_ERR
            else:
                pinfo("Success: %s's crontab => OK" %user)
            try:
                os.unlink(filen)
            except:
                perror('Could not remove temp file: %s' %filen)
                pass
        return r

    def fix_crontabs(self):
        r = RET_OK
        for ck in self.checks:
             if not ck['valid']:
                 perror("Pattern '%s' for %s's crontab is absent in the requested content" %(ck['check'], ck['user']))
                 r |= RET_ERR
                 continue
             pr = RET_ERR
             lines = self.crontabs[ck['user']].split('\n')
             for line in lines:
                 if line.startswith('#'):
                     continue
                 if ck['check'] in line:
                     pr = RET_OK
                     break
             if pr != RET_OK:
                 pinfo("Trying to add to %s's crontab, entry: %s" %(ck['user'], ck['add'].strip('\n')))
                 self.crontabs[ck['user']] += ck['add']
                 self.upds[ck['user']] = 1
             r |= pr
        r |= self.add_2_crontabs()
        return r

    def fix(self):
        r = 0
        r != self.fix_crontabs()
        return r


if __name__ == "__main__":
    main(CrontabEntry)
