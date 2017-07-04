#!/usr/bin/env python

"""
OSVC_COMP_MPATH='[{"key": "defaults.polling_interval", "op": ">=", "value": 20}, {"key": "device.HP.HSV210.prio", "op": "=", "value": "alua"}]' ./linux.mpath.py OSVC_COMP_MPATH check
"""

import os
import sys
import json

sys.path.append(os.path.dirname(__file__))

import sys
from comp import *

try:
    import ConfigParser
except ImportError:
    import configparser as ConfigParser


class Ini(object):
    def __init__(self, prefix='OSVC_COMP_KEYVAL_', path=None):
        self.prefix = prefix.upper()
        self.cf = path
        self.nocf = False
        self.changed = False
        if path is None:
            perror("no file path specified")
            raise NotApplicable()

        self.keys = []
        for k in [ key for key in os.environ if key.startswith(self.prefix)]:
            try:
                l = json.loads(os.environ[k])
            except ValueError:
                perror('key syntax error on var[', k, '] = ',os.environ[k])
                raise ComplianceError

            # validate keys
            for key in l:
                if 'key' not in key:
                    perror("'key' is not defined in %s" % str(key))
                    raise ComplianceError
                try:
                    section, keyname = key['key'].split('.')
                except:
                    perror('%s key should be defined as <section>.<keyname>' % key['key'])
                    raise ComplianceError

            self.keys += l

        if len(self.keys) == 0:
            pinfo("no applicable variable found in rulesets", self.prefix)
            raise NotApplicable()

        self.conf = ConfigParser.RawConfigParser()
        try:
            self.conf.read(self.cf)
        except Exception as e:
            perror("failed to parse %s: %s" % (self.cf, str(e)))
            raise ComplianceError


    def fixable(self):
        return RET_OK

    def _check_key(self, keyname, target, op, value, verbose=True):
        r = RET_OK
        if op == "unset":
            if value is not None:
                if verbose:
                    perror("%s is set, should not be"%keyname)
                return RET_ERR
            else:
                if verbose:
                    pinfo("%s is not set, on target"%keyname)
                return RET_OK

        if value is None:
            if verbose:
                perror("%s is not set, target: %s"%(keyname, str(target)))
            return RET_ERR

        if type(value) == list:
            if str(target) in value:
                if verbose:
                    pinfo("%s=%s on target"%(keyname, str(value)))
                return RET_OK
            else:
                if verbose:
                    perror("%s=%s is not set"%(keyname, str(target)))
                return RET_ERR

        if op == '=':
            if str(value) != str(target):
                if verbose:
                    perror("%s=%s, target: %s"%(keyname, str(value), str(target)))
                r |= RET_ERR
            elif verbose:
                pinfo("%s=%s on target"%(keyname, str(value)))
        else:
            if type(value) != int:
                if verbose:
                    perror("%s=%s value must be integer"%(keyname, str(value)))
                r |= RET_ERR
            elif op == '<=' and value > target:
                if verbose:
                    perror("%s=%s target: <= %s"%(keyname, str(value), str(target)))
                r |= RET_ERR
            elif op == '>=' and value < target:
                if verbose:
                    perror("%s=%s target: >= %s"%(keyname, str(value), str(target)))
                r |= RET_ERR
            elif verbose:
                pinfo("%s=%s on target"%(keyname, str(value)))
        return r

    def check_key(self, key, verbose=True):
        if 'key' not in key:
            if verbose:
                perror("'key' not set in rule %s"%str(key))
            return RET_NA
        if 'value' not in key:
            if verbose:
                perror("'value' not set in rule %s"%str(key))
            return RET_NA
        if 'op' not in key:
            op = "="
        else:
            op = key['op']
        target = key['value']

        if op not in ('>=', '<=', '=', 'unset'):
            if verbose:
                perror("'op' must be either '=', '>=' or '<=': %s"%str(key))
            return RET_NA

        section, keyname = key['key'].split('.')
        if type(target) == int:
            f = self.conf.getint
        elif type(target) == bool:
            f = self.conf.getboolean
        else:
            f = self.conf.get

        try:
            value = f(section, keyname)
        except ConfigParser.NoSectionError:
            value = None

        r = self._check_key(keyname, target, op, value, verbose=verbose)
        return r

    def fix_key(self, key):
        section, keyname = key['key'].split('.')
        if key['op'] == "unset":
            pinfo("%s unset"%key['key'])
            self.conf.unset(section, keyname)
            self.changed = True
        else:
            if not self.conf.has_section(section):
                self.conf.add_section(section)
            pinfo("%s=%s set"%(key['key'], key['value']))
            self.conf.set(section, keyname, key['value'])
            self.changed = True

    def check(self):
        r = 0
        for key in self.keys:
            r |= self.check_key(key, verbose=True)
        return r

    def fix(self):
        for key in self.keys:
            if self.check_key(key, verbose=False) == RET_ERR:
                self.fix_key(key)
        if not self.changed:
            return
        try:
            fp = open(self.cf, 'w')
            self.conf.write(fp)
            fp.close()
        except Exception as e:
            perror("failed to write %s"%self.cf)
            perror(e)
            return RET_ERR
        return RET_OK

if __name__ == "__main__":
    syntax = """syntax:
      %s PREFIX check|fixable|fix configfile_path"""%sys.argv[0]
    if len(sys.argv) != 4:
        perror("wrong number of arguments")
        perror(syntax)
        sys.exit(RET_ERR)
    try:
        o = Ini(sys.argv[1], sys.argv[3])
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

