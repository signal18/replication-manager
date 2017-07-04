#!/usr/bin/env python

import os
import sys

from subprocess import *

sys.path.append(os.path.dirname(__file__))

from comp import *

class Hosts(object):
    def __init__(self, host, method):
        self.host = host
        self.method = method.upper()
        self.fqdn = ''
        self.ip = ''
        self.cn = ''
        self.shorthost = ''

    def fixable(self):
        return RET_ERR

    def fix(self):
        perror('Manual fix required for %s in %s' %(self.host, self.method))
        return RET_NA

    def get_from_hosts(self):
        cmd = ['/usr/bin/getent', 'hosts', self.host]
        p = Popen(cmd, stdout=PIPE, stderr=PIPE)
        out,err = p.communicate()
        if p.returncode != 0 :
            return False
        out = bdecode(out)
        d = False
        lines = out.splitlines()
        for line in lines:
            if self.host in line:
                t = line.split()
                i = 1
                while i < len(t):
                    if self.host == t[i]:
                        d = True
                        self.ip = t[0]
                        if '.' in self.host:
                            self.fqdn = t[i]
                        else:
                            self.shorthost = t[i]
                    if self.host+'.' in t[i]:
                        d = True
                        self.fqdn = t[i]
                    i += 1
        return d

    def get_from_etchosts(self):
        cmd = ['/usr/bin/getent', 'hosts']
        p = Popen(cmd, stdout=PIPE, stderr=PIPE)
        out,err = p.communicate()
        if p.returncode != 0 :
            return False
        d = False
        out = bdecode(out)
        lines = out.splitlines()
        for line in lines:
            if self.host in line:
                t = line.split()
                i = 1
                while i < len(t):
                    if self.host == t[i]:
                        d = True
                        self.ip = t[0]
                        if '.' in self.host:
                            self.fqdn = t[i]
                        else:
                            self.shorthost = t[i]
                    if self.host+'.' in t[i]:
                        d = True
                        self.fqdn = t[i]
                    i += 1
        return d

    def get_from_dns(self):
        sld = False
        if self.host.endswith('.'):
            sld = True
        cmd = ['/usr/sbin/nslookup', self.host]
        p = Popen(cmd, stdout=PIPE, stderr=PIPE)
        out,err = p.communicate()
        if p.returncode != 0 :
            perror(' '.join(cmd))
            raise ComplianceError()

        out = bdecode(out)
        d = False
        lines = out.splitlines()
        for line in lines:
            if line.startswith(self.host+'.') and 'canonical name =' in line and not d:
                d = True
                t = line.split()
                self.fqdn = t[0]
                if '.' not in self.host:
                    self.shorthost = self.host
                continue
            if line.startswith('Name:'):
                t = line.split()
                if d:
                    self.cn = t[1]
                else:
                    self.fqdn = t[1]
                    if self.fqdn == self.host:
                        d = True
                    if sld and self.fqdn+'.' == self.host:
                        d = True
                    elif '.' not in self.host:
                        hn = t[1].split('.')
                        if hn[0] == self.host:
                            self.shorthost = self.host
                            d = True
                continue
            if line.startswith('Address:') and d:
                t = line.split()
                self.ip = t[1]
                return d
        return d

    def get_ip_from_dns(self):
        if len(self.ip) < 7:
            perror('Bad IP address [%s] for %s' %(self.ip, self.host))
            return False
        cmd = ['/usr/sbin/host', self.ip]
        p = Popen(cmd, stdout=PIPE, stderr=PIPE)
        out,err = p.communicate()
        if p.returncode != 0 :
            perror(' '.join(cmd))
            raise ComplianceError()
        out = bdecode(out)
        lines = out.splitlines()
        for line in lines:
            if '.in-addr.arpa' in line and 'domain name pointer' in line:
                if line.split()[-1].strip('.') == self.fqdn:
                    return True
        return False

    def check_dns(self):
        r = RET_OK
        if self.get_from_dns():
            pinfo('%s [%s] is defined in DNS => OK' %(self.host, self.fqdn))
        else :
            perror('DNS does not define %s' %self.host)
            r |= RET_ERR
        if self.get_ip_from_dns():
            pinfo('IP=%s is defined in DNS for %s' %(self.ip, self.fqdn))
        else:
            perror('IP=%s is NOT DEFINED in DNS for %s' %(self.ip, self.fqdn))
            r |= RET_ERR
        return r

    def check_hosts(self):
        if self.get_from_hosts():
            pinfo('%s is known in hosts database => OK' %self.host)
            return RET_OK
        else:
            perror('Unknown host %s in hosts database' %self.host)
            return RET_ERR

    def check_etchosts(self):
        if self.get_from_etchosts():
            pinfo('%s is defined in /etc/hosts => OK' %self.host)
            return RET_OK
        else:
            perror('Unknown host %s in /etc/hosts' %self.host)
            return RET_ERR

    def check(self):
        r = RET_ERR
        if self.method == 'DNS':
            r = self.check_dns()
        elif self.method == 'HOSTS':
            r = self.check_hosts()
        elif self.method == 'LOCAL':
            r = self.check_etchosts()
        else:
            perror('Unknown METHOD: "%s"' %self.method)
        return r

if __name__ == "__main__":
    syntax = """syntax:
      %s check|fixable|fix host-name method"""%sys.argv[0]
    if len(sys.argv) != 4:
        perror("wrong number of arguments")
        perror(syntax)
        sys.exit(RET_ERR)
    action = sys.argv[1]
    host = sys.argv[2]
    method = sys.argv[3].upper()
    try:
        o = Hosts(host, method)
        if action == 'check':
            RET = o.check()
        elif action == 'fix':
            RET = o.fix()
        elif action == 'fixable':
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
