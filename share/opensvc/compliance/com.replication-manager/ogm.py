#!/usr/bin/env python

import os
import sys
import tempfile
from subprocess import *
import json

sys.path.append(os.path.dirname(__file__))

import files
from comp import *
from utilities import which

class Ogm(object):
    def __init__(self, prefix='OSVC_COMP_OGM_INST_'):
        self.prefix = prefix.upper()
        self.data = []
        self.ogm_script = "/home/ogm/ogm"

        for k in [ key for key in os.environ if key.startswith(self.prefix)]:
            try:
                d = json.loads(os.environ[k])
                self.data.append(d)
            except ValueError:
                print >>sys.stderr, 'failed to parse variable', os.environ[k]
            except Exception, e:
                print >>sys.stderr, \
                  'unknown error parsing variable', os.environ[k], \
                  ": ", str(e)

        if len(self.data) == 0:
            raise NotApplicable()

    def get_ogm(self):
        if not which(self.ogm_script):
            r = files.CompFiles(prefix="OSVC_COMP_OGM_SCRIPT").fix()
            if r != 0:
                raise Exception()

    def fixable(self):
        return RET_OK

    def check_inst(self, inst, verbose=False):
        product = inst.get("paquet")
        if product is None:
            if verbose: print >>sys.stderr, "PAQUET is not set"
            return 1
        cmd = [self.ogm_script, "-li"]
        if "OSVC_COMP_SERVICES_SVC_NAME" in os.environ:
            cmd += ["-N /" + os.environ.get("OSVC_COMP_SERVICES_SVC_NAME")]
        p = Popen(cmd, stdout=PIPE, stderr=None)
        out, err = p.communicate()
        if product not in out:
            if verbose: print >>sys.stderr, product, "is not installed"
            return 1
        if verbose: print product, "is installed"
        return 0

    def fix_inst(self, inst):
        user = inst.get("compte_produit")
        if user is None:
            print >>sys.stderr, "COMPTE_PRODUIT is not set"
            return 1
        fd = tempfile.NamedTemporaryFile(delete=False)
        name = fd.name
        buff = ""
        for key in inst:
            buff += "export %s=%s\n"%("XISMW_"+key.toupper(), inst[key])
        fd.write(buff)
        fd.close()
        print "generated config file:"
        print buff
        cmd = ["su", user, "-c"]
        wcmd = self.ogm_script + " -x -e " + name
        if "OSVC_COMP_SERVICES_SVC_NAME" in os.environ:
            wcmd += " -N /" + os.environ.get("OSVC_COMP_SERVICES_SVC_NAME")
        cmd.append(wcmd)
        print " ".join(cmd)
        p = Popen(cmd)
        p.communicate()
        os.unlink(name)
        if p.returncode != 0:
            return 1
        return 0

    def check(self):
        try:
            self.get_ogm()
        except:
            return 1
        r = 0
        for inst in self.data:
            r |= self.check_inst(inst, verbose=True)
        return r

    def fix(self):
        try:
            self.get_ogm()
        except:
            return 1
        r = 0
        for inst in self.data:
            if self.check_inst(inst, verbose=False) == RET_ERR:
                r += self.fix_inst(inst)
        return r

if __name__ == "__main__":
    syntax = """syntax:
      %s PREFIX check|fixable|fix"""%sys.argv[0]
    if len(sys.argv) != 3:
        print >>sys.stderr, "wrong number of arguments"
        print >>sys.stderr, syntax
        sys.exit(RET_ERR)
    try:
        o = Ogm(sys.argv[1])
        if sys.argv[2] == 'check':
            RET = o.check()
        elif sys.argv[2] == 'fix':
            RET = o.fix()
        elif sys.argv[2] == 'fixable':
            RET = o.fixable()
        else:
            print >>sys.stderr, "unsupported argument '%s'"%sys.argv[2]
            print >>sys.stderr, syntax
            RET = RET_ERR
    except NotApplicable:
        sys.exit(RET_NA)
    except:
        import traceback
        traceback.print_exc()
        sys.exit(RET_ERR)

    sys.exit(RET)

