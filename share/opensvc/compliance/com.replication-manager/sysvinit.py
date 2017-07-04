#!/usr/bin/env python

from subprocess import *
import os
import sys
import glob
import re

sys.path.append(os.path.dirname(__file__))

from comp import *

class InitError(Exception):
    pass

class UnknownService(Exception):
    pass

class SetError(Exception):
    pass

class SeqError(Exception):
    pass

class DupError(Exception):
    pass

class SysVInit(object):
    def __init__(self):
        self.load()

    def __str__(self):
        s = ""
        for svc in self.services:
            s += "%-20s %s\n"%(svc, ' '.join(map(lambda x: '%-4s'%x,  str(self.services[svc]))))
        return s


    def get_svcname(self, s):
        _s = os.path.basename(s)
        _svcname = re.sub(r'^[SK][0-9]+', '', _s)
        _seq = re.sub(r'[KS](\d+).+', r'\1', _s)
        if _s[0] == 'S':
            _state = 'on'
        elif _s[0] == 'K':
            _state = 'off'
        else:
            raise InitError("unexepected service name: %s"%s)
        return _state, _seq, _svcname

    def load(self):
        self.services = {}
        self.levels = (0, 1, 2, 3, 4, 5, 6)
        default = "none"

        self.base_d = "/etc"
        self.init_d = self.base_d + "/init.d"
        if not os.path.exists(self.init_d):
            self.base_d = "/sbin"
            self.init_d = self.base_d + "/init.d"
        if not os.path.exists(self.init_d):
            raise InitError("init dir not found")

        for l in self.levels:
            for s in glob.glob("%s/rc%d.d/[SK]*"%(self.base_d, l)):
                state, seq, svc = self.get_svcname(s)
                if svc not in self.services:
                    self.services[svc] = {seq: [default, default, default, default, default, default, default]}
                if seq not in self.services[svc]:
                    self.services[svc][seq] = [default, default, default, default, default, default, default]
                self.services[svc][seq][l] = state

    def activate(self, service, levels, seq):
        for l in levels:
            self.activate_one(service, levels, seq)

    def activate_one(self, service, level, seq):
        if len(service) == 0:
            SetError("service is empty")

        start_l = "S%s%s"%(seq,service)
        svc_p = "../init.d/"+service

        os.chdir(self.base_d+"/rc%s.d"%level)

        g = glob.glob("[SK]*%s"%service)
        if len(g) > 0:
            cmd = ['rm', '-f'] + g
            pinfo(" ".join(cmd))
            p = Popen(cmd, stdout=PIPE)
            out, err = p.communicate()
            if p.returncode != 0:
                raise SetError()

        cmd = ['ln', '-sf', svc_p, start_l]
        pinfo(" ".join(cmd))
        p = Popen(cmd, stdout=PIPE)
        out, err = p.communicate()
        if p.returncode != 0:
            raise SetError()

    def deactivate_one(self, service, level, seq):
        if len(service) == 0:
            SetError("service is empty")
        stop_l = "K%s%s"%(seq,service)
        svc_p = "../init.d/"+service

        os.chdir(self.base_d+"/rc%s.d"%level)

        g = glob.glob("[SK]*%s"%service)
        if len(g) > 0:
            cmd = ['rm', '-f'] + g
            pinfo(" ".join(cmd))
            p = Popen(cmd, stdout=PIPE)
            out, err = p.communicate()
            if p.returncode != 0:
                raise SetError()

        cmd = ['ln', '-sf', svc_p, stop_l]
        pinfo(" ".join(cmd))
        p = Popen(cmd, stdout=PIPE)
        out, err = p.communicate()
        if p.returncode != 0:
            raise SetError()

    def delete_one(self, service, level):
        if len(service) == 0:
            SetError("service is empty")
        g = glob.glob(self.base_d+"/rc%s.d"%level+"/*"+service)
        if len(g) == 0:
            return
        cmd = ['rm', '-f'] + g
        pinfo(" ".join(cmd))
        p = Popen(cmd, stdout=PIPE)
        out, err = p.communicate()
        if p.returncode != 0:
            raise SetError()

    def check_init(self, service):
        init_f = os.path.join(self.init_d, service)
        if os.path.exists(init_f):
            return True
        return False

    def set_state(self, service, level, state, seq):
        if service in self.services and seq in self.services[service]:
            curstates = self.services[service][seq]

            if state != "del" and len(curstates) == 1 and curstates[int(level)] == state or \
               state == "del" and len(curstates) == 1 and curstates[int(level)] == "none":
                return

        if state == "on":
            self.activate_one(service, level, seq)
        elif state == "off":
            self.deactivate_one(service, level, seq)
        elif state == "del":
            self.delete_one(service, level)
        else:
            raise SetError()

    def get_state(self, service, level, seq):
        if service not in self.services:
            raise UnknownService()

        # compute the number of different launcher for this service in the runlevel
        l = []
        for _seq in self.services[service]:
            if self.services[service][_seq][level] != "none":
                l.append(self.services[service][_seq][level])

        if seq is None:
            if len(l) == 0:
                return "none"
            raise SeqError()

        if len(l) > 1:
            raise DupError()

        try:
            curstates = self.services[service][seq]
            curstate = curstates[int(level)]
        except:
            curstate = "none"

        if len(l) == 1 and curstate == "none":
            raise SeqError()
        return curstate

    def check_state(self, service, levels, state, seq=None, verbose=False):
        r = 0
        if seq is not None and type(seq) == int:
            seq = "%02d"%seq

        if not self.check_init(service):
            if verbose:
                perror("service %s init script does not exist in %s"%(service, self.init_d))
            r |= 1

        if seq is None and state != "del":
            if verbose:
                perror("service %s sequence number must be set"%(service))
            return 1

        for level in levels:
            try:
                level = int(level)
            except:
                continue
            try:
                curstate = self.get_state(service, level, seq)
            except DupError:
                if verbose:
                    perror("service %s has multiple launchers at level %d"%(service, level))
                r |= 1
                continue
            except SeqError:
                if verbose:
                    perror("service %s sequence number error at level %d"%(service, level))
                r |= 1
                continue
            except UnknownService:
                curstate = "none"

            if (state != "del" and curstate != state) or \
               (state == "del" and curstate != "none"):
                if verbose:
                    perror("service", service, "at runlevel", level, "is in state", curstate, "! target state is", state)
                r |= 1
            else:
                if verbose:
                    pinfo("service", service, "at runlevel", level, "is in state", curstate)
        return r
            
    def fix_state(self, service, levels, state, seq=None):
        if seq is not None and type(seq) == int:
            seq = "%02d"%seq

        if seq is None and state != "del":
            perror("service %s sequence number must be set"%(service))
            return 1

        for level in levels:
            try:
                self.set_state(service, level, state, seq)
            except SetError:
                perror("failed to set", service, "runlevels")
                return 1
        return 0

if __name__ == "__main__":
    o = SysVInit()
    pinfo(o)
    pinfo('xfs@rc3 =', o.get_state('xfs', 3))

