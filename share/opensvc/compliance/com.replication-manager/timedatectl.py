#!/usr/bin/env python

data = {
  "default_prefix": "OSVC_COMP_TIMEDATECTL_",
  "example_value": """
    {
      "timezone": "Europe/Paris",
      "ntpenabled": "no"
    }
  """,
  "description": """* Checks timedatectl settings
* Module need to be called with the exposed target settings as variable (timedatectl.py OSVC_COMP_TIMEDATECTL_1 check)
""",
  "form_definition": """
Desc: |
  A timedatectl rule, fed to the 'timedatectl' compliance object to setup rhel/centos7+ timezone/ntp.
Css: comp48

Outputs:
  -
    Dest: compliance variable
    Class: timedatectl
    Type: json
    Format: dict

Inputs:
  -
    Id: timezone
    Label: Timezone
    DisplayModeLabel: timezone
    LabelCss: action16
    Mandatory: No
    Help: 'The timezone name, as listed by "timedatectl list-timezones" command. Example: Europe/Paris'
    Type: string

  -
    Id: ntpenabled
    Label: NTP Enabled
    DisplayModeLabel: ntpenabled
    LabelCss: time16
    Mandatory: No
    Default: "yes"
    Candidates:
      - "yes"
      - "no"
    Help: "Specify yes or no, to request enabling or disabling the chronyd time service, driven through timedatectl command."
    Type: string
"""
}

import os
import sys
from subprocess import *

sys.path.append(os.path.dirname(__file__))

from comp import *
from utilities import *

class CompTimeDateCtl(CompObject):
    def __init__(self, prefix=None):
        CompObject.__init__(self, prefix=prefix, data=data)

    def init(self):
        self.sysname, self.nodename, x, x, self.machine = os.uname()

        self.inputs = self.get_rules()[0]

        if self.sysname not in ['Linux']:
            perror('module not supported on', self.sysname)
            raise NotApplicable()

        if which('timedatectl') is None:
            perror('timedatectl command not found', self.sysname)
            raise NotApplicable()

        self.tz = self.get_valid_tz()
        self.live = self.get_current_tdctl()

    def get_current_tdctl(self):
        """
        [root@rhel71 averon]# timedatectl
              Local time: mar. 2016-03-29 17:13:43 CEST
          Universal time: mar. 2016-03-29 15:13:43 UTC
                RTC time: mar. 2016-03-29 15:13:42
               Time zone: Europe/Paris (CEST, +0200)
             NTP enabled: yes
        NTP synchronized: yes
         RTC in local TZ: no
              DST active: yes
         Last DST change: DST began at
                          dim. 2016-03-27 01:59:59 CET
                          dim. 2016-03-27 03:00:00 CEST
         Next DST change: DST ends (the clock jumps one hour backwards) at
                          dim. 2016-10-30 02:59:59 CEST
                          dim. 2016-10-30 02:00:00 CET
        """

        current = {}
        try:
            cmd = ['timedatectl', 'status']
            p = Popen(cmd, stdout=PIPE)
            out, err = p.communicate()
            if p.returncode != 0:
                raise
            out = bdecode(out)
            for line in out.splitlines():
                if 'Time zone:' in line:
                    s = line.split(':')[-1].strip()
                    t = s.split(' ')[0]
                    current['timezone'] = t
                if 'NTP enabled:' in line:
                    current['ntpenabled'] = line.split(':')[-1].strip()
        except:
            perror('can not fetch timedatectl infos')
            return None
        return current

    def get_valid_tz(self):
        tz = []
        try:
            cmd = ['timedatectl', '--no-pager', 'list-timezones']
            p = Popen(cmd, stdout=PIPE)
            out, err = p.communicate()
            if p.returncode != 0:
                raise
            out = bdecode(out)
            for line in out.splitlines():
                curtz = line.strip()
                if curtz is not '':
                    tz.append(curtz)
        except:
            perror('can not build valid timezone list')
            return None
        return tz

    def fixable(self):
        return RET_NA

    def check(self):
        if self.live is None:
            return RET_NA
        r = RET_OK
        for input in self.inputs:
            r |= self._check(input)
        return r

    def _check(self, input):
        if self.inputs[input] == self.live[input]:
            pinfo("timedatectl %s is %s, on target" % (input, self.live[input] ))
            return RET_OK
        perror("timedatectl %s is %s, target %s" % (input, self.live[input], self.inputs[input]))
        return RET_ERR

    def set_tz(self, timezone):
        try:
            cmd = ['timedatectl', 'set-timezone', timezone]
            p = Popen(cmd, stdout=PIPE)
            out, err = p.communicate()
            if p.returncode != 0:
                raise
        except:
            perror('could not set timezone')
            return None
        return RET_OK

    def set_ntp(self, value):
        try:
            cmd = ['timedatectl', 'set-ntp', value]
            p = Popen(cmd, stdout=PIPE)
            out, err = p.communicate()
            if p.returncode != 0:
                raise
        except:
            perror('could not set ntp')
            return None
        return RET_OK

    def _fix(self, input):
        r = RET_OK
        if input in 'timezone':
            r |= self.set_tz(self.inputs[input])
            return r
        if input in 'ntpenabled':
            r |= self.set_ntp(self.inputs[input])
            return r
        return RET_NA

    def fix(self):
        r = RET_OK
        if self.check() == RET_ERR:
            for input in self.inputs:
                r |= self._fix(input)
        return r

    def test(self):
        print("Not Implemented")

if __name__ == "__main__":
    main(CompTimeDateCtl)
