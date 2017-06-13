#!/usr/bin/env python

import os
import sys
import json
from distutils.version import LooseVersion as V
from subprocess import *

sys.path.append(os.path.dirname(__file__))

from comp import *

class CompFirmware(object):
    def __init__(self, var):
        self.versions = {}

        if var not in os.environ:
            pinfo(var, 'not found in environment')
            raise NotApplicable()

        try:
            self.target_versions = json.loads(os.environ[var])
        except:
            perror(var, 'misformatted variable:', os.environ[var])
            raise NotApplicable()
        for key in self.target_versions:
            if type(self.target_versions[key]) != list:
                continue
            self.target_versions[key] = list(map(lambda x: str(x), self.target_versions[key]))

        self.sysname, self.nodename, x, x, self.machine = os.uname()

        if self.sysname not in ['Linux']:
            perror('module not supported on', self.sysname)
            raise NotApplicable()

    def get_versions(self):
        self.get_bios_version_Linux()
        self.get_qla_version_Linux()
        self.get_lpfc_version_Linux()

    def get_qla_version_Linux(self):
        self.versions['qla2xxx'] = None
        self.versions['qla2xxx_fw'] = None
        import glob
        hosts = glob.glob('/sys/bus/pci/drivers/qla2*/*:*:*/host*')
        if len(hosts) == 0:
            return
        hosts_proc = map(lambda x: '/proc/scsi/qla2xxx/'+os.path.basename(x).replace('host', ''), hosts)
        hosts = map(lambda x: '/sys/class/fc_host/'+os.path.basename(x)+'/symbolic_name', hosts)
        for i, host in enumerate(hosts):
            if os.path.exists(host):
                with open(host, 'r') as f:
                    buff = f.read()
                l = buff.split()
                for e in l:
                    if e.startswith("DVR:"):
                        self.versions['qla2xxx'] = e.replace("DVR:", "")
                    elif e.startswith("FW:"):
                        v = e.replace("FW:", "")
                        # store the lowest firmware version
                        if self.versions['qla2xxx_fw'] is None or V(self.versions['qla2xxx_fw']) > V(v):
                            self.versions['qla2xxx_fw'] = v
            elif os.path.exists(hosts_proc[i]):
                with open(hosts_proc[i], 'r') as f:
                    buff = f.read()
                for line in buff.split('\n'):
                    if "Firmware version" not in line:
                        continue
                    l = line.split()
                    n_words = len(l)
                    idx = l.index("Driver") + 2
                    if idx <= n_words:
                        self.versions['qla2xxx'] = l[idx]
                    idx = l.index("Firmware") + 2
                    if idx <= n_words:
                        v = l[idx]
                        if self.versions['qla2xxx_fw'] is None or V(self.versions['qla2xxx_fw']) > V(v):
                            self.versions['qla2xxx_fw'] = v

    def get_lpfc_version_Linux(self):
        self.versions['lpfc'] = None
        self.versions['lpfc_fw'] = None
        import glob
        hosts = glob.glob('/sys/class/scsi_host/host*/fwrev')
        if len(hosts) == 0:
            return
        for host in hosts:
            with open(host, 'r') as f:
                buff = f.read()
            l = buff.split()
            if self.versions['lpfc_fw'] is None or V(self.versions['lpfc_fw']) > V(l[0]):
                self.versions['lpfc_fw'] = l[0]

        if self.versions['lpfc_fw'] is None:
            # no need to fetch module version if no hardware
            return

        cmd = ['modinfo', 'lpfc']
        p = Popen(cmd, stdout=PIPE)
        out, err = p.communicate()
        if p.returncode != 0:
            return
        out = bdecode(out)
        for line in out.splitlines():
            if line.startswith('version:'):
                self.versions['lpfc'] = line.split()[1]
                return

    def get_bios_version_Linux(self):
        p = os.path.join(os.sep, 'sys', 'class', 'dmi', 'id', 'bios_version')
        try:
            f = open(p, 'r')
            ver = f.read().strip()
            f.close()
            self.versions['server'] = ver
            return
        except:
            pass

        try:
            cmd = ['dmidecode']
            p = Popen(cmd, stdout=PIPE, stderr=PIPE)
            out, err = p.communicate()
            if p.returncode != 0:
                raise
            out = bdecode(out)
            for line in out.splitlines():
                if 'Version:' in line:
                    self.versions['server'] = line.split(':')[-1].strip()
                    return
            raise
        except:
            pinfo('can not fetch bios version')
            return

    def fixable(self):
        return RET_NA

    def check(self):
        self.get_versions()
        r = RET_OK
        for key in self.target_versions:
            if key not in self.versions:
                perror("TODO: get", key, "version")
                continue
            if type(self.versions[key]) not in (str, unicode):
                pinfo("no", key)
                continue
            if type(self.target_versions[key]) == list and \
               self.versions[key] not in self.target_versions[key]:
                perror(key, "version is %s, target %s"%(self.versions[key], ' or '.join(self.target_versions[key])))
                r |= RET_ERR
            elif type(self.target_versions[key]) != list and \
                 self.versions[key] != self.target_versions[key]:
                perror(key, "version is %s, target %s"%(self.versions[key], self.target_versions[key]))
                r |= RET_ERR
            else:
                pinfo(key, "version is %s, on target"%self.versions[key])
                continue
        return r

    def fix(self):
        return RET_NA

if __name__ == "__main__":
    syntax = """syntax:
      %s TARGET check|fixable|fix"""%sys.argv[0]
    if len(sys.argv) != 3:
        perror("wrong number of arguments")
        perror(syntax)
        sys.exit(RET_ERR)
    try:
        o = CompFirmware(sys.argv[1])
        if sys.argv[2] == 'check':
            RET = o.check()
        elif sys.argv[2] == 'fix':
            RET = o.fix()
        elif sys.argv[2] == 'fixable':
            RET = o.fixable()
        else:
            perror("unsupported argument '%s'"%sys.argv[2])
            perror(syntax)
            RET = RET_ERR
    except NotApplicable:
        sys.exit(RET_NA)
    except:
        import traceback
        traceback.print_exc()
        sys.exit(RET_ERR)

    sys.exit(RET)

