#!/usr/bin/env python

data = {
  "default_prefix": "OSVC_COMP_CRON_ENTRY_",
  "example_value": "add:osvc:* * * * *:/path/to/mycron:/etc/cron.d/opensvc",
  "description": """* Add and Remove cron entries
* Support arbitrary cron file location
""",
}

import os
import sys
import shutil
import glob
from subprocess import *

sys.path.append(os.path.dirname(__file__))

from comp import *

class CompCron(CompObject):
    def __init__(self, prefix=None):
        CompObject.__init__(self, prefix=prefix, data=data)

    def init(self):
        self.sysname, self.nodename, x, x, self.machine = os.uname()

        if self.sysname == 'SunOS' :
            self.crontab_locs = [
                '/var/spool/cron/crontabs'
            ]
        else:
            self.crontab_locs = [
                '/etc/cron.d',
                '/var/spool/cron/crontabs',
                '/var/spool/cron',
                '/var/cron/tabs',
            ]

        self.ce = []
        for _ce in self.get_rules_raw():
                e = _ce.split(':')
                if len(e) < 5:
                    perror("malformed variable %s. format: action:user:sched:cmd:[file]"%_ce)
                    continue
                if e[0] not in ('add', 'del'):
                    perror("unsupported action in variable %s. set 'add' or 'del'"%_ce)
                    continue
                if len(e[2].split()) != 5:
                    perror("malformed schedule in variable %s"%_ce)
                    continue
                self.ce += [{
                        'var': _ce,
                        'action': e[0],
                        'user': e[1],
                        'sched': e[2],
                        'cmd': e[3],
                        'file': e[4],
                       }]

        if len(self.ce) == 0:
            raise NotApplicable()


    def activate_cron(self, cron_file):
        """ Activate changes (actually only needed on HP-UX)
        """
        if '/var/spool/' in cron_file:
            pinfo("tell crond about the change")
            cmd = ['crontab', cron_file]
            process = Popen(cmd, stdout=PIPE, stderr=PIPE, close_fds=True)
            buff = process.communicate()

    def fixable(self):
        r = RET_OK
        for e in self.ce:
            try:
                self._fixable_cron(e)
            except ComplianceError, e:
                perror(str(e))
                r = RET_ERR
            except Unfixable, e:
                perror(str(e))
                return r
        return r

    def fix(self):
        r = RET_OK
        for e in self.ce:
            try:
                if e['action'] == 'add':
                    self._add_cron(e)
                elif e['action'] == 'del':
                    self._del_cron(e)
            except ComplianceError, e:
                perror(str(e))
                r = RET_ERR
            except Unfixable, e:
                perror(str(e))
                return r
        return r

    def check(self):
        r = RET_OK
        for e in self.ce:
            try:
                self._check_cron(e)
            except ComplianceError, e:
                perror(str(e))
                r = RET_ERR
            except Unfixable, e:
                perror(str(e))
                return r
        return r

    def get_cron_file(self, e):
        """ order of preference
        """
        cron_file = None
        for loc in self.crontab_locs:
            if not os.path.exists(loc):
                continue
            if loc == '/etc/cron.d':
                 cron_file = os.path.join(loc, e['file'])
            else:
                 cron_file = os.path.join(loc, e['user'])
            break
        return cron_file

    def format_entry(self, cron_file, e):
        if 'cron.d' in cron_file:
            s = ' '.join([e['sched'], e['user'], e['cmd']])
        else:
            s = ' '.join([e['sched'], e['cmd']])
        return s

    def _fixable_cron(self, e):
        cron_file = self.get_cron_file(e)

        if cron_file is None:
            raise Unfixable("no crontab usual location found (%s)"%str(self.crontab_locs))

    def _check_cron(self, e):
        cron_file = self.get_cron_file(e)

        if cron_file is None:
            raise Unfixable("no crontab usual location found (%s)"%str(self.crontab_locs))

        s = self.format_entry(cron_file, e)

        if not os.path.exists(cron_file):
            raise ComplianceError("cron entry not found '%s' in '%s'"%(s, cron_file))

        with open(cron_file, 'r') as f:
            new = f.readlines()
            found = False
            for line in new:
                if s == line[:-1]:
                     found = True
                     break
            if not found and e['action'] == 'add':
                raise ComplianceError("wanted cron entry not found: '%s' in '%s'"%(s, cron_file))
            if found and e['action'] == 'del':
                raise ComplianceError("unwanted cron entry found: '%s' in '%s'"%(s, cron_file))

    def _del_cron(self, e):
        cron_file = self.get_cron_file(e)

        if cron_file is None:
            raise Unfixable("no crontab usual location found (%s)"%str(self.crontab_locs))

        s = self.format_entry(cron_file, e)

        if not os.path.exists(cron_file):
            return

        new = []
        with open(cron_file, 'r') as f:
            lines = f.readlines()
            for line in lines:
                if s == line[:-1]:
                    pinfo("delete entry '%s' from '%s'"%(s, cron_file))
                    continue
                new.append(line)

        if len(new) == 0:
            pinfo('deleted last entry of %s. delete file too.'%cron_file)
            os.unlink(cron_file)
        else:
            with open(cron_file, 'w') as f:
                f.write(''.join(new))
            self.activate_cron(cron_file)

    def _add_cron(self, e):
        cron_file = self.get_cron_file(e)

        if cron_file is None:
            raise Unfixable("no crontab usual location found (%s)"%str(self.crontab_locs))

        s = self.format_entry(cron_file, e)

        new = False
        if os.path.exists(cron_file):
            with open(cron_file, 'r') as f:
                new = f.readlines()
                found = False
                for line in new:
                    if s == line[:-1]:
                        found = True
                        break
                if not found:
                    new.append(s+'\n')
        else:
            new = [s+'\n']

        if not new:
            raise ComplianceError("problem preparing the new crontab '%s'"%cron_file)

        pinfo("add entry '%s' to '%s'"%(s, cron_file))
        with open(cron_file, 'w') as f:
            f.write(''.join(new))
        self.activate_cron(cron_file)

if __name__ == "__main__":
    main(CompCron)

