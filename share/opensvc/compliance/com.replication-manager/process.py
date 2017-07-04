#!/usr/bin/env python

data = {
  "default_prefix": "OSVC_COMP_PROC_",
  "example_value": """
[
  {
    "comm": "foo",
    "uid": 2345,
    "state": "on",
    "user": "foou"
  },
  {
    "comm": "bar",
    "state": "off",
    "uid": 2345
  }
]
""",
  "description": """* Checks if a process is present, specifying its comm, and optionnaly its owner's uid and/or username.
""",
  "form_definition": """
Desc: |
  A rule defining a process that should be running or not running on the target host, its owner's username and the command to launch it or to stop it.
Css: comp48
Outputs:
  -
    Dest: compliance variable
    Type: json
    Format: list of dict
    Class: process
Inputs:
  -
    Id: comm
    Label: Command
    DisplayModeLabel: comm
    LabelCss: action16
    Mandatory: No
    Type: string
    Help: The Unix process command, as shown in the ps comm column.
  -
    Id: args
    Label: Arguments
    DisplayModeLabel: args
    LabelCss: action16
    Mandatory: No
    Type: string
    Help: The Unix process arguments, as shown in the ps args column.
  -
    Id: state
    Label: State
    DisplayModeLabel: state
    LabelCss: action16
    Type: string
    Mandatory: Yes
    Default: on
    Candidates:
      - "on"
      - "off"
    Help: The expected process state.
  -
    Id: uid
    Label: Owner user id
    DisplayModeLabel: uid
    LabelCss: guy16
    Type: integer
    Help: The Unix user id owning the process.
  -
    Id: user
    Label: Owner user name
    DisplayModeLabel: user
    LabelCss: guy16
    Type: string
    Help: The Unix user name owning the process.
  -
    Id: start
    Label: Start command
    DisplayModeLabel: start
    LabelCss: action16
    Type: string
    Help: The command to start or stop the process, including the executable arguments. The executable must be defined with full path.
""",
}

import os
import sys
import json
import re
from subprocess import *

sys.path.append(os.path.dirname(__file__))

from comp import *
from utilities import which

class CompProcess(CompObject):
    def __init__(self, prefix=None):
        CompObject.__init__(self, prefix=prefix, data=data)

    def init(self):
        self.sysname, self.nodename, x, x, self.machine = os.uname()

        if self.sysname not in ['Linux', 'AIX', 'SunOS', 'FreeBSD', 'Darwin', 'HP-UX']:
            perror('module not supported on', self.sysname)
            raise NotApplicable()

        if self.sysname == 'HP-UX' and 'UNIX95' not in os.environ:
            os.environ['UNIX95'] = ""

        self.process = self.get_rules()
        self.validate_process()

        if len(self.process) == 0:
            raise NotApplicable()

        self.load_ps()

    def load_ps_args(self):
        self.ps_args = {}
        cmd = ['ps', '-e', '-o', 'pid,uid,user,args']
        p = Popen(cmd, stdout=PIPE)
        out, err = p.communicate()
        if p.returncode != 0:
            perror("unable to fetch ps")
            raise ComplianceError
        out = bdecode(out)
        lines = out.splitlines()
        if len(lines) < 2:
            return
        for line in lines[1:]:
            l = line.split()
            if len(l) < 4:
                continue
            pid, uid, user = l[:3]
            args = " ".join(l[3:])
            if args not in self.ps_args:
                self.ps_args[args] = [(pid, int(uid), user)]
            else:
                self.ps_args[args].append((pid, int(uid), user))

    def load_ps_comm(self):
        self.ps_comm = {}
        cmd = ['ps', '-e', '-o', 'comm,pid,uid,user']
        p = Popen(cmd, stdout=PIPE)
        out, err = p.communicate()
        if p.returncode != 0:
            perror("unable to fetch ps")
            raise ComplianceError
        out = bdecode(out)
        lines = out.splitlines()
        if len(lines) < 2:
            return
        for line in lines[1:]:
            l = line.split()
            if len(l) != 4:
                continue
            comm, pid, uid, user = l
            if comm not in self.ps_comm:
                self.ps_comm[comm] = [(pid, int(uid), user)]
            else:
                self.ps_comm[comm].append((pid, int(uid), user))

    def load_ps(self):
        self.load_ps_comm()
        self.load_ps_args()

    def validate_process(self):
        l = []
        for process in self.process:
            if self._validate_process(process) == RET_OK:
                l.append(process)
        self.process = l

    def _validate_process(self, process):
        if 'comm' not in process and 'args' not in process:
            perror(process, 'rule is malformed ... nor comm nor args key present')
            return RET_ERR
        if 'uid' in process and type(process['uid']) != int:
            perror(process, 'rule is malformed ... uid value must be integer')
            return RET_ERR
        return RET_OK

    def get_keys_args(self, args):
        found = []
        for key in self.ps_args:
            if re.match(args, key) is not None:
                found.append(key)
        return found

    def get_keys_comm(self, comm):
        found = []
        for key in self.ps_comm:
            if re.match(comm, key) is not None:
                found.append(key)
        return found

    def check_present_args(self, args, verbose):
        if len(args.strip()) == 0:
            return RET_OK
        found = self.get_keys_args(args)
        if len(found) == 0:
            if verbose:
                perror('process with args', args, 'is not started ... should be')
            return RET_ERR
        else:
            if verbose:
                pinfo('process with args', args, 'is started ... on target')
        return RET_OK

    def check_present_comm(self, comm, verbose):
        if len(comm.strip()) == 0:
            return RET_OK
        found = self.get_keys_comm(comm)
        if len(found) == 0:
            if verbose:
                perror('process with command', comm, 'is not started ... should be')
            return RET_ERR
        else:
            if verbose:
                pinfo('process with command', comm, 'is started ... on target')
        return RET_OK

    def check_present(self, process, verbose):
        r = RET_OK
        if 'comm' in process:
            r |= self.check_present_comm(process['comm'], verbose)
        if 'args' in process:
            r |= self.check_present_args(process['args'], verbose)
        return r

    def check_not_present_comm(self, comm, verbose):
        if len(comm.strip()) == 0:
            return RET_OK
        found = self.get_keys_comm(comm)
        if len(found) == 0:
           if verbose:
               pinfo('process with command', comm, 'is not started ... on target')
           return RET_OK
        else:
           if verbose:
               perror('process with command', comm, 'is started ... shoud be')
        return RET_ERR

    def check_not_present_args(self, args, verbose):
        if len(args.strip()) == 0:
            return RET_OK
        found = self.get_keys_args(args)
        if len(found) == 0:
           if verbose:
               pinfo('process with args', args, 'is not started ... on target')
           return RET_OK
        else:
           if verbose:
               perror('process with args', args, 'is started ... shoud be')
        return RET_ERR

    def check_not_present(self, process, verbose):
        r = 0
        if 'comm' in process:
            r |= self.check_not_present_comm(process['comm'], verbose)
        if 'args' in process:
            r |= self.check_not_present_args(process['args'], verbose)
        return r

    def check_process(self, process, verbose=True):
        r = RET_OK
        if process['state'] == 'on':
            r |= self.check_present(process, verbose)
            if r == RET_ERR:
                return RET_ERR
            if 'uid' in process:
                r |= self.check_uid(process, process['uid'], verbose)
            if 'user' in process:
                r |= self.check_user(process, process['user'], verbose)
        else:
            r |= self.check_not_present(process, verbose)

        return r

    def check_uid(self, process, uid, verbose):
        if 'args' in process:
            return self.check_uid_args(process['args'], uid, verbose)
        if 'comm' in process:
            return self.check_uid_comm(process['comm'], uid, verbose)

    def check_uid_comm(self, comm, uid, verbose):
        if len(comm.strip()) == 0:
            return RET_OK
        found = False
        keys = self.get_keys_comm(comm)
        for key in keys:
            for _pid, _uid, _user in self.ps_comm[key]:
                if uid == _uid:
                    found = True
                    continue
        if found:
            if verbose:
                pinfo('process with command', comm, 'runs with uid', _uid, '... on target')
        else:
            if verbose:
                perror('process with command', comm, 'does not run with uid', _uid, '... should be')
            return RET_ERR
        return RET_OK

    def check_uid_args(self, args, uid, verbose):
        if len(args.strip()) == 0:
            return RET_OK
        found = False
        keys = self.get_keys_args(args)
        for key in keys:
            for _pid, _uid, _user in self.ps_args[key]:
                if uid == _uid:
                    found = True
                    continue
        if found:
            if verbose:
                pinfo('process with args', args, 'runs with uid', _uid, '... on target')
        else:
            if verbose:
                perror('process with args', args, 'does not run with uid', _uid, '... should be')
            return RET_ERR
        return RET_OK

    def check_user(self, process, user, verbose):
        if 'args' in process:
            return self.check_user_args(process['args'], user, verbose)
        if 'comm' in process:
            return self.check_user_comm(process['comm'], user, verbose)

    def check_user_comm(self, comm, user, verbose):
        if len(comm.strip()) == 0:
            return RET_OK
        if user is None or len(user) == 0:
            return RET_OK
        found = False
        keys = self.get_keys_comm(comm)
        for key in keys:
            for _pid, _uid, _user in self.ps_comm[key]:
                if user == _user:
                    found = True
                    continue
        if found:
            if verbose:
                pinfo('process with command', comm, 'runs with user', _user, '... on target')
        else:
            if verbose:
                perror('process with command', comm, 'runs with user', _user, '... should run with user', user)
            return RET_ERR
        return RET_OK

    def check_user_args(self, args, user, verbose):
        if len(args.strip()) == 0:
            return RET_OK
        if user is None or len(user) == 0:
            return RET_OK
        found = False
        keys = self.get_keys_args(args)
        for key in keys:
            for _pid, _uid, _user in self.ps_args[key]:
                if user == _user:
                    found = True
                    continue
        if found:
            if verbose:
                pinfo('process with args', args, 'runs with user', _user, '... on target')
        else:
            if verbose:
                perror('process with args', args, 'runs with user', _user, '... should run with user', user)
            return RET_ERR
        return RET_OK

    def fix_process(self, process):
        if process['state'] == 'on':
            if self.check_present(process, verbose=False) == RET_OK:
                if ('uid' in process and self.check_uid(process, process['uid'], verbose=False) == RET_ERR) or \
                   ('user' in process and self.check_user(process, process['user'], verbose=False) == RET_ERR):
                    perror(process, "runs with the wrong user. can't fix.")
                    return RET_ERR
                return RET_OK
        elif process['state'] == 'off':
            if self.check_not_present(process, verbose=False) == RET_OK:
                return RET_OK

        if 'start' not in process or len(process['start'].strip()) == 0:
            perror("undefined fix method for process", process['comm'])
            return RET_ERR

        v = process['start'].split(' ')
        if not which(v[0]):
            perror("fix command", v[0], "is not present or not executable")
            return RET_ERR
        pinfo('exec:', process['start'])
        try:
            p = Popen(v, stdout=PIPE, stderr=PIPE)
            out, err = p.communicate()
        except Exception as e:
            perror(e)
            return RET_ERR
        out = bdecode(out)
        err = bdecode(err)
        if len(out) > 0:
            pinfo(out)
        if len(err) > 0:
            perror(err)
        if p.returncode != 0:
            perror("fix up command returned with error code", p.returncode)
            return RET_ERR
        return RET_OK

    def check(self):
        r = 0
        for process in self.process:
            r |= self.check_process(process)
        return r

    def fix(self):
        r = 0
        for process in self.process:
            r |= self.fix_process(process)
        return r

if __name__ == "__main__":
    main(CompProcess)
