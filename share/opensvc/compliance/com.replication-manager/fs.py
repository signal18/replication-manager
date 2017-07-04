#!/usr/bin/env python
""" 
Verify file content. The collector provides the format with
wildcards. The module replace the wildcards with contextual
values.

The variable format is json-serialized:

[{
 "dev": "lv_applisogm",
 "size": "1024M",
 "mnt": "/%%ENV:SVCNAME%%/applis/ogm",
 "vg": ["%%ENV:SVCNAME%%", "vgAPPLIS", "vgCOMMUN01", "vgLOCAL"]
}]

Wildcards:
%%ENV:VARNAME%%		Any environment variable value

Toggle:
%%ENV:FS_STRIP_SVCNAME_FROM_DEV_IF_IN_VG%%

"""

import os
import sys
import json
import stat
import re
from subprocess import *
from stat import *

sys.path.append(os.path.dirname(__file__))

from comp import *
from utilities import which

class CompFs(object):
    def __init__(self, prefix='OSVC_COMP_FS_'):
        self.prefix = prefix.upper()
        self.sysname, self.nodename, x, x, self.machine = os.uname()
        self.sysname = self.sysname.replace('-', '')
        self.fs = []
        self.res = {}
        self.res_status = {}

        if 'OSVC_COMP_SERVICES_SVCNAME' in os.environ:
            self.svcname = os.environ['OSVC_COMP_SERVICES_SVCNAME']
            self.osvc_service = True
        else:
            os.environ['OSVC_COMP_SERVICES_SVCNAME'] = ""
            self.svcname = None
            self.osvc_service = False

        keys = [key for key in os.environ if key.startswith(self.prefix)]
        if len(keys) == 0:
            raise NotApplicable()

        self.vglist()

        for k in keys:
            try:
                self.fs += self.add_fs(os.environ[k])
            except ValueError:
                perror('failed to parse variable', os.environ[k])

        if len(self.fs) == 0:
            raise NotApplicable()

        self.fs.sort(lambda x, y: cmp(x['mnt'], y['mnt']))


    def vglist_HPUX(self):
        import glob
        l = glob.glob("/dev/*/group")
        l = map(lambda x: x.split('/')[2], l)
        self.vg = l

    def vglist_Linux(self):
        if not which("vgs"):
            perror('vgs command not found')
            raise ComplianceError()
        cmd = ['vgs', '-o', 'vg_name', '--noheadings']
        p = Popen(cmd, stdout=PIPE, stderr=PIPE)
        out, err = p.communicate()
        if p.returncode != 0:
            perror('failed to list volume groups')
            raise ComplianceError()
        out = bdecode(out)
        self.vg = out.split()

    def vglist(self):
        if not hasattr(self, 'vglist_'+self.sysname):
            perror(self.sysname, 'not supported')
            raise NotApplicable()
        getattr(self, 'vglist_'+self.sysname)()
        
    def subst(self, v):
        if type(v) == list:
            l = []
            for _v in v:
                l.append(self.subst(_v))
            return l
        if type(v) != str and type(v) != unicode:
            return v

        p = re.compile('%%ENV:\w+%%')
        for m in p.findall(v):
            s = m.strip("%").replace('ENV:', '')
            if s in os.environ:
                _v = os.environ[s]
            elif 'OSVC_COMP_'+s in os.environ:
                _v = os.environ['OSVC_COMP_'+s]
            else:
                perror(s, 'is not an env variable')
                raise NotApplicable()
            v = v.replace(m, _v)
        return v.strip()

    def add_fs(self, v):
        if type(v) == str or type(v) == unicode:
            d = json.loads(v)
        else:
            d = v
        l = []

        # recurse if multiple fs are specified in a list of dict
        if type(d) == list:
            for _d in d:
                l += self.add_fs(_d)
            return l

        if type(d) != dict:
            perror("not a dict:", d)
            return l

        if 'dev' not in d:
            perror('dev should be in the dict:', d)
            return l
        if 'mnt' not in d:
            perror('mnt should be in the dict:', d)
            return l
        if 'size' not in d:
            perror('size should be in the dict:', d)
            return l
        if 'vg' not in d:
            perror('vg should be in the dict:', d)
            return l
        if 'type' not in d:
            perror('type should be in the dict:', d)
            return l
        if 'opts' not in d:
            perror('opts should be in the dict:', d)
            return l
        if type(d['vg']) != list:
            d['vg'] = [d['vg']]

        d['vg_orig'] = d['vg']
        d['vg'] = self.subst(d['vg'])
        d['prefvg'] = self.prefvg(d)
        d['dev'] = self.strip_svcname(d)

        for k in ('dev', 'mnt', 'size', 'type', 'opts'):
            d[k] = self.subst(d[k])

        d['mnt'] = self.normpath(d['mnt'])
        d['devpath'] = self.devpath(d)
        d['rdevpath'] = self.rdevpath(d)
        try:
            d['size'] = self.size_to_mb(d)
        except ComplianceError:
            return []

        return [d]

    def strip_svcname(self, fs):
        key = "OSVC_COMP_FS_STRIP_SVCNAME_FROM_DEV_IF_IN_VG"
        if key not in os.environ or os.environ[key] != "true":
            return fs['dev']
        if "%%ENV:SERVICES_SVCNAME%%" not in fs['vg_orig'][fs['prefvg_idx']]:
            return fs['dev']

        # the vg is dedicated to the service. no need to embed
        # the service name in the lv name too
        s = fs['dev'].replace("%%ENV:SERVICES_SVCNAME%%", "")
        if s == "lv_":
            s = "root"
        return s

    def normpath(self, p):
        l = p.split('/')
        p = os.path.normpath(os.path.join(os.sep, *l))
        return p

    def rdevpath(self, d):
        return '/dev/%s/r%s'%(d['prefvg'], d['dev'])

    def devpath(self, d):
        return '/dev/%s/%s'%(d['prefvg'], d['dev'])

    def prefvg(self, d):
        lc_candidate_vg = map(lambda x: x.lower(), d['vg'])
        lc_existing_vg = map(lambda x: x.lower(), self.vg)
        for i, vg in enumerate(lc_candidate_vg):
            if vg in lc_existing_vg:
                d['prefvg_idx'] = i
                # return capitalized vg name
                return self.vg[lc_existing_vg.index(vg)]
        perror("no candidate vg is available on this node for dev %s"%d['dev'])
        raise NotApplicable()

    def check_fs_mnt(self, fs, verbose=False):
        if not os.path.exists(fs['mnt']):
            if verbose:
                perror("mount point", fs['mnt'], "does not exist")
            return 1
        if verbose:
            pinfo("mount point", fs['mnt'], "exists")
        return 0

    def check_fs_dev_exists(self, fs, verbose=False):
        if not os.path.exists(fs['devpath']):
            if verbose:
                perror("device", fs['devpath'], "does not exist")
            return 1
        if verbose:
            pinfo("device", fs['devpath'], "exists")
        return 0

    def check_fs_dev_stat(self, fs, verbose=False):
        mode = os.stat(fs['devpath'])[ST_MODE]
        if not S_ISBLK(mode):
            if verbose:
                perror("device", fs['devpath'], "is not a block device")
            return 1
        if verbose:
            pinfo("device", fs['devpath'], "is a block device")
        return 0

    def find_vg_rid(self, vgname):
        rids = [ rid for rid in self.res_status.keys() if rid.startswith('vg#') ]
        for rid in rids:
            if self.get_res_item(rid, 'vgname') == vgname:
                return rid
        return None

    def private_svc_vg_down(self, fs):
        if self.svcname is None or not self.osvc_service:
            return False
        rid = self.find_vg_rid(fs['prefvg'])
        if rid is None:
            # vg is not driven by the service
            return False
        if self.res_status[rid] not in ('up', 'stdby up'):
            return False
        return True

    def check_fs_dev(self, fs, verbose=False):
        if self.private_svc_vg_down(fs):
            # don't report error on passive node with private svc prefvg
            return 0
        if self.check_fs_dev_exists(fs, verbose) == 1:
            return 1
        if self.check_fs_dev_stat(fs, verbose) == 1:
            return 1
        return 0

    def fix_fs_dev(self, fs):
        if self.check_fs_dev(fs, False) == 0:
            return 0
        if self.check_fs_dev_exists(fs, False) == 0:
            perror("device", fs['devpath'], "already exists. won't fix.")
            return 1
        return self.createlv(fs)

    def createlv(self, fs):
        if not hasattr(self, 'createlv_'+self.sysname):
            perror(self.sysname, 'not supported')
            raise NotApplicable()
        return getattr(self, 'createlv_'+self.sysname)(fs)

    def size_to_mb(self, fs):
        s = fs['size']
        unit = s[-1]
        size = int(s[:-1])
        if unit == 'T':
            s = str(size*1024*1024)
        elif unit == 'G':
            s = str(size*1024)
        elif unit == 'M':
            s = str(size)
        elif unit == 'K':
            s = str(size//1024)
        else:
            perror("unknown size unit in rule: %s (use T, G, M or K)"%s)
            raise ComplianceError()
        return s

    def createlv_HPUX(self, fs):
        cmd = ['lvcreate', '-n', fs['dev'], '-L', fs['size'], fs['prefvg']]
        pinfo(' '.join(cmd))
        p = Popen(cmd, stdout=PIPE, stderr=PIPE)
        out, err = p.communicate()
        out = bdecode(out)
        err = bdecode(err)
        if len(out) > 0:
            pinfo(out)
        if len(err) > 0:
            pinfo(err)
        if p.returncode != 0:
            return 1
        return 0

    def createlv_Linux(self, fs):
        os.environ["LVM_SUPPRESS_FD_WARNINGS"] = "1"
        cmd = ['lvcreate', '-n', fs['dev'], '-L', fs['size']+'M', fs['prefvg']]
        pinfo(' '.join(cmd))
        p = Popen(cmd, stdout=PIPE, stderr=PIPE)
        out, err = p.communicate()
        out = bdecode(out)
        err = bdecode(err)
        if len(out) > 0:
            pinfo(out)
        if len(err) > 0:
            pinfo(err)
        if p.returncode != 0:
            return 1
        return 0

    def fix_fs_mnt(self, fs, verbose=False):
        if self.check_fs_mnt(fs, False) == 0:
            return 0
        pinfo("create", fs['mnt'], "mount point")
        os.makedirs(fs['mnt'])
        return 0

    def check_fs_fmt_HPUX_vxfs(self, fs, verbose=False):
        cmd = ['fstyp', fs['devpath']]
        p = Popen(cmd, stdout=PIPE, stderr=PIPE)
        out, err = p.communicate()
        out = bdecode(out)
        err = bdecode(err)
        if p.returncode != 0 or "vxfs" not in out:
            if verbose:
                perror(fs['devpath'], "is not formatted")
            return 1
        if verbose:
            pinfo(fs['devpath'], "is correctly formatted")
        return 0

    def check_fs_fmt_HPUX(self, fs, verbose=False):
        if fs['type'] == 'vxfs':
            return self.check_fs_fmt_HPUX_vxfs(fs, verbose)
        perror("unsupported fs type: %s"%fs['type'])
        return 1

    def check_fs_fmt_Linux(self, fs, verbose=False):
        if fs['type'] in ('ext2', 'ext3', 'ext4'):
            return self.check_fs_fmt_Linux_ext(fs, verbose)
        perror("unsupported fs type: %s"%fs['type'])
        return 1

    def check_fs_fmt_Linux_ext(self, fs, verbose=False):
        cmd = ['tune2fs', '-l', fs['devpath']]
        p = Popen(cmd, stdout=PIPE, stderr=PIPE)
        out, err = p.communicate()
        out = bdecode(out)
        err = bdecode(err)
        if p.returncode != 0:
            if verbose:
                perror(fs['devpath'], "is not formatted")
            return 1
        if verbose:
            pinfo(fs['devpath'], "is correctly formatted")
        return 0

    def fix_fs_fmt_Linux_ext(self, fs):
        cmd = ['mkfs.'+fs['type'], '-q', '-b', '4096', fs['devpath']]
        pinfo(' '.join(cmd))
        p = Popen(cmd, stdout=PIPE, stderr=PIPE)
        out, err = p.communicate()
        out = bdecode(out)
        err = bdecode(err)
        if len(out) > 0:
            pinfo(out)
        if len(err) > 0:
            pinfo(err)
        if p.returncode != 0:
            return 1

        cmd = ['tune2fs', '-m', '0', '-c', '0', '-i', '0', fs['devpath']]
        pinfo(' '.join(cmd))
        p = Popen(cmd, stdout=PIPE, stderr=PIPE)
        out, err = p.communicate()
        out = bdecode(out)
        err = bdecode(err)
        if len(out) > 0:
            pinfo(out)
        if len(err) > 0:
            pinfo(err)
        if p.returncode != 0:
            return 1

        return 0

    def fix_fs_fmt_Linux(self, fs):
        if fs['type'] in ('ext2', 'ext3', 'ext4'):
            return self.fix_fs_fmt_Linux_ext(fs)
        perror("unsupported fs type: %s"%fs['type'])
        return 1

    def check_fs_fmt(self, fs, verbose=False):
        if not hasattr(self, 'check_fs_fmt_'+self.sysname):
            perror(self.sysname, 'not supported')
            raise NotApplicable()
        return getattr(self, 'check_fs_fmt_'+self.sysname)(fs, verbose)

    def fix_fs_fmt_HPUX_vxfs(self, fs):
        cmd = ['newfs', '-F', 'vxfs', '-b', '8192', fs['rdevpath']]
        pinfo(' '.join(cmd))
        p = Popen(cmd, stdout=PIPE, stderr=PIPE)
        out, err = p.communicate()
        out = bdecode(out)
        err = bdecode(err)
        if len(out) > 0:
            pinfo(out)
        if len(err) > 0:
            pinfo(err)
        if p.returncode != 0:
            return 1
        return 0

    def fix_fs_fmt_HPUX(self, fs):
        if fs['type'] == 'vxfs':
            return self.fix_fs_fmt_HPUX_vxfs(fs)
        perror("unsupported fs type: %s"%fs['type'])
        return 1

        if not hasattr(self, 'check_fs_fmt_'+self.sysname):
            perror(self.sysname, 'not supported')
            raise NotApplicable()
        return getattr(self, 'check_fs_fmt_'+self.sysname)(fs, verbose)

    def fix_fs_fmt(self, fs):
        if self.check_fs_fmt(fs) == 0:
            return 0
        if not hasattr(self, 'fix_fs_fmt_'+self.sysname):
            perror(self.sysname, 'not supported')
            raise NotApplicable()
        return getattr(self, 'fix_fs_fmt_'+self.sysname)(fs)

    def get_res_item(self, rid, item):
        cmd = ['svcmgr', '-s', self.svcname, 'get', '--param', '.'.join((rid, item))]
        p = Popen(cmd, stdout=PIPE, stderr=PIPE)
        out, err = p.communicate()
        out = bdecode(out)
        err = bdecode(err)
        if p.returncode != 0:
            perror(' '.join(cmd), 'failed')
            return 1
        return out.strip()
 
    def get_res(self, rid):
        if rid in self.res:
            return self.res[rid]
        d = {}
        d['mnt'] = self.get_res_item(rid, 'mnt')
        d['dev'] = self.get_res_item(rid, 'dev')
        self.res[rid] = d
        return d

    def get_fs_rids(self, refresh=False):
        if not refresh and hasattr(self, 'rids'):
            return self.rids
        cmd = ['svcmgr', '-s', self.svcname, 'json_status']
        p = Popen(cmd, stdout=PIPE, stderr=PIPE)
        out, err = p.communicate()
        out = bdecode(out)
        err = bdecode(err)
        for line in out.splitlines():
            if line.startswith('{'):
                out = line
                break
        try:
            # json_status returns 0, even when it outs no data
            self.res_status = json.loads(out)['resources']
        except Exception as e:
            pinfo(e)
            pinfo(out)
            self.rids = []
            self.osvc_service = False
            return self.rids
        self.rids = [ k for k in self.res_status.keys() if k.startswith('fs#') ]
        return self.rids

    def find_rid(self, fs):
        found = False
        for rid in self.rids:
            d = self.get_res(rid)
            if d['mnt'] == fs['mnt'] and d['dev'] == fs['devpath']:
                return rid
        return None

    def fix_fs_local(self, fs):
        if self.svcname is not None and self.osvc_service:
            return 0
        if self.check_fs_local(fs, False) == 0:
            return 0
        with open("/etc/fstab", "r") as f:
            lines = f.read().split('\n')
        if len(lines[-1]) == 0:
            del(lines[-1])
        p = re.compile(r'\s*%s\s+'%(fs['devpath']))
        newline = "%s %s %s %s 0 2"%(fs['devpath'], fs['mnt'], fs['type'], fs['opts'])
        for i, line in enumerate(lines):
            if line == newline:
                return 0
            if re.match(p, line) is not None:
                pinfo("remove '%s' from fstab"%line)
                del lines[i]
        lines.append(newline)
        pinfo("append '%s' to fstab"%newline)
        try:
            with open("/etc/fstab", "w") as f:
                f.write("\n".join(lines)+'\n')
        except:
            perror("failed to rewrite fstab")
            return 1
        pinfo("fstab rewritten")
        return 0

    def check_fs_local(self, fs, verbose=False):
        if self.svcname is not None and self.osvc_service:
            return 0
        p = re.compile(r'\s*%s\s+%s'%(fs['devpath'], fs['mnt']))
        with open("/etc/fstab", "r") as f:
            buff = f.read()
        if re.search(p, buff) is not None:
            if verbose:
                pinfo("%s@%s resource correctly set in fstab"%(fs['mnt'], fs['devpath']))
                return 0
        if verbose:
            perror("%s@%s resource correctly set in fstab"%(fs['mnt'], fs['devpath']))
        return 1

    def check_fs_svc(self, fs, verbose=False):
        if self.svcname is None:
            return 0
        rids = self.get_fs_rids()
        if not self.osvc_service:
            return 0
        rid = self.find_rid(fs)
        if rid is None:
            if verbose:
                perror("%s@%s resource not found in service %s"%(fs['mnt'], fs['devpath'], self.svcname))
            return 1
        if verbose:
            pinfo("%s@%s resource correctly set in service %s"%(fs['mnt'], fs['devpath'], self.svcname))
        return 0
            
    def fix_fs_svc(self, fs):
        if not self.osvc_service or self.check_fs_svc(fs, False) == 0:
            return 0
        cmd = ['svcmgr', '-s', self.svcname, 'get', '--param', 'DEFAULT.encapnodes']
        p = Popen(cmd, stdout=PIPE, stderr=PIPE)
        out, err = p.communicate()
        out = bdecode(out)
        err = bdecode(err)
        if self.nodename in out.strip().split():
            tags = "encap"
        else:
            tags = ''
        cmd = ['svcmgr', '-s', self.svcname, 'update', '--resource',
               '{"rtype": "fs", "mnt": "%s", "dev": "%s", "type": "%s", "mnt_opt": "%s", "tags": "%s"}'%(fs['mnt'], fs['devpath'], fs['type'], fs['opts'], tags)]
        pinfo(' '.join(cmd))
        p = Popen(cmd, stdout=PIPE, stderr=PIPE)
        out, err = p.communicate()
        out = bdecode(out)
        err = bdecode(err)
        if p.returncode != 0:
            perror("unable to fetch %s json status"%self.svcname)
            return 1
        return 0

    def check_fs_mounted(self, fs, verbose=False):
        if os.path.ismount(fs['mnt']):
            if verbose:
                pinfo(fs['mnt'], "is mounted")
            return 0
        if verbose:
            perror(fs['mnt'], "is not mounted")
        return 1

    def fix_fs_mounted(self, fs):
        if self.check_fs_mounted(fs, False) == 0:
            return 0
        if self.svcname is None or not self.osvc_service:
            return self.fix_fs_mounted_local(fs)
        else:
            return self.fix_fs_mounted_svc(fs)

    def fix_fs_mounted_svc(self, fs):
        rids = self.get_fs_rids(refresh=True)
        rid = self.find_rid(fs)
        if rid is None:
            perror("fs resource with mnt=%s not found in service %s"%(fs['mnt'], self.svcname))
            return 1
        cmd = ['svcmgr', '-s', self.svcname, '--rid', rid, 'mount', '--cluster']
        p = Popen(cmd, stdout=PIPE, stderr=PIPE)
        out, err = p.communicate()
        out = bdecode(out)
        err = bdecode(err)
        if p.returncode != 0 and "unsupported action" in err:
            cmd = ['svcmgr', '-s', self.svcname, '--rid', rid, 'startfs', '--cluster']
            p = Popen(cmd, stdout=PIPE, stderr=PIPE)
            out, err = p.communicate()
        pinfo(' '.join(cmd))
        if p.returncode != 0:
            perror("unable to mount %s"%fs['mnt'])
            return 1
        return 0

    def fix_fs_mounted_local(self, fs):
        cmd = ['mount', fs['mnt']]
        pinfo(' '.join(cmd))
        p = Popen(cmd, stdout=PIPE, stderr=PIPE)
        out, err = p.communicate()
        out = bdecode(out)
        err = bdecode(err)
        if len(out) > 0:
            pinfo(out)
        if len(err) > 0:
            perror(err)
        if p.returncode != 0:
            perror("unable to mount %s"%fs['mnt'])
            return 1
        return 0

    def check_fs(self, fs, verbose=False):
        r = 0
        r |= self.check_fs_mnt(fs, verbose)
        r |= self.check_fs_dev(fs, verbose)
        r |= self.check_fs_fmt(fs, verbose)
        r |= self.check_fs_svc(fs, verbose)
        r |= self.check_fs_local(fs, verbose)
        r |= self.check_fs_mounted(fs, verbose)
        return r

    def fix_fs(self, fs):
        if self.fix_fs_mnt(fs) != 0:
            return 1
        if self.fix_fs_dev(fs) != 0:
            return 1
        if self.fix_fs_fmt(fs) != 0:
            return 1
        if self.fix_fs_svc(fs) != 0:
            return 1
        if self.fix_fs_local(fs) != 0:
            return 1
        if self.fix_fs_mounted(fs) != 0:
            return 1
        return 0

    def fixable(self):
        return RET_NA

    def check(self):
        r = 0
        for f in self.fs:
            r |= self.check_fs(f, verbose=True)
        return r

    def fix(self):
        r = 0
        for f in self.fs:
            r |= self.fix_fs(f)
        return r


if __name__ == "__main__":
    syntax = """syntax:
      %s PREFIX check|fixable|fix"""%sys.argv[0]
    if len(sys.argv) != 3:
        perror("wrong number of arguments")
        perror(syntax)
        sys.exit(RET_ERR)
    try:
        o = CompFs(sys.argv[1])
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
    except ComplianceError:
        sys.exit(RET_ERR)
    except:
        import traceback
        traceback.print_exc()
        sys.exit(RET_ERR)

    sys.exit(RET)

