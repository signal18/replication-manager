#!/usr/bin/env python

from subprocess import *
import sys
import os

sys.path.append(os.path.dirname(__file__))

from comp import *

os.environ['LANG'] = 'C'

class InitError(Exception):
    pass

class UnknownService(Exception):
    pass

class SetError(Exception):
    pass

class Chkconfig(object):
    def __init__(self):
        self.load()

    def __str__(self):
        s = ""
        for svc in self.services:
            s += "%-20s %s\n"%(svc, ' '.join(map(lambda x: '%-4s'%x,  self.services[svc])))
        return s

    def load(self):
        self.services = {}

        p = Popen(['/sbin/chkconfig', '--list'], stdout=PIPE, stderr=PIPE)
        out, err = p.communicate()
        if p.returncode != 0:
            raise InitError()
        out = bdecode(out)
        for line in out.splitlines():
            words = line.split()
            if len(words) != 8:
                continue
            self.services[words[0]] = []
            for w in words[1:]:
                level, state = w.split(':')
                self.services[words[0]].append(state)

    def load_one(self, service):
        p = Popen(['/sbin/chkconfig', '--list', service], stdout=PIPE, stderr=PIPE)
        out, err = p.communicate()
        if p.returncode != 0:
            out = bdecode(out)
            if 'not referenced' in out:
                self.services[service] = ['off', 'off', 'off', 'off', 'off', 'off']
                return
            raise InitError()

    def activate(self, service):
        p = Popen(['chkconfig', service, 'on'], stdout=PIPE, stderr=PIPE)
        out, err = p.communicate()
        if p.returncode != 0:
            raise SetError()

    def set_state(self, service, level, state):
        curstate = self.get_state(service, level)
        if curstate == state:
            return
        p = Popen(['chkconfig', '--level', level, service, state], stdout=PIPE, stderr=PIPE)
        out, err = p.communicate()
        if p.returncode != 0:
            raise SetError()

    def get_state(self, service, level):
        if service not in self.services:
            try:
                self.load_one(service)
            except InitError:
                pass

        if service not in self.services:
            raise UnknownService()

        return self.services[service][level]

    def check_state(self, service, levels, state, seq=None, verbose=False):
        r = 0
        for level in levels:
            try:
                level = int(level)
            except:
                continue
            try:
                curstate = self.get_state(service, level)
            except UnknownService:
                if verbose:
                    perror("can not get service", service, "runlevels")
                return 1
            if curstate != state:
                if verbose:
                    perror("service", service, "at runlevel", level, "is in state", curstate, "! target state is", state)
                r |= 1
            else:
                if verbose:
                    pinfo("service", service, "at runlevel", level, "is in state", curstate)
        return r
            
    def fix_state(self, service, levels, state, seq=None):
        cmd = ['chkconfig', '--level', levels, service, state]
        pinfo("exec:", ' '.join(cmd))
        p = Popen(cmd, stdout=PIPE, stderr=PIPE)
        out, err = p.communicate()
        if p.returncode != 0:
            perror("failed to set", service, "runlevels")
            pinfo(out)
            perror(err)
            return 1
        return 0

if __name__ == "__main__":
    o = Chkconfig()
    pinfo(o)
    pinfo('xfs@rc3 =', o.get_state('xfs', 3))
