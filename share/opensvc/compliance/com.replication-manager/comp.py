#!/usr/bin/env python

from __future__ import print_function
import sys
import os
import re
import json
import base64

if sys.version_info[0] >= 3:
    from urllib.request import Request, urlopen
    from urllib.error import HTTPError
    from urllib.parse import urlencode
else:
    from urllib2 import Request, urlopen
    from urllib2 import HTTPError
    from urllib import urlencode

RET_OK = 0
RET_ERR = 1
RET_NA = 2

RET = RET_OK

class NotApplicable(Exception):
     pass

class Unfixable(Exception):
     pass

class ComplianceError(Exception):
     pass

class InitError(Exception):
     pass

class EndRecursion(Exception):
     pass

def pinfo(*args, **kwargs):
    if is_string(args) and len(args):
        return
    if isinstance(args, list) and (len(args) == 0 or len(args[0]) == 0):
        return
    kwargs["file"] = sys.stdout
    print(*args, **kwargs)

def perror(*args, **kwargs):
    if is_string(args) and len(args):
        return
    if isinstance(args, list) and (len(args) == 0 or len(args[0]) == 0):
        return
    kwargs["file"] = sys.stderr
    print(*args, **kwargs)

def is_string(s):
    """ python[23] compatible
    """
    if sys.version_info[0] == 2:
        l = (str, unicode)
    else:
        l = (str)
    if isinstance(s, l):
        return True
    return False

def bdecode(buff):
    if sys.version_info[0] < 3:
        return buff
    else:
        try:
            return str(buff, "utf-8")
        except:
            return str(buff, "ascii")
    return buff

def bencode(buff):
    if sys.version_info[0] < 3:
        return buff
    else:
        try:
            return bytes(buff, "utf-8")
        except:
            return bytes(buff, "ascii")
    return buff

class CompObject(object):
    def __init__(self,
                 prefix=None,
                 data={}):
        if prefix:
            self.prefix = prefix.upper()
        elif "default_prefix" in data:
            self.prefix = data["default_prefix"].upper()
        else:
            self.prefix = "MAGIX12345"

        self.extra_syntax_parms = data.get("extra_syntax_parms")
        self.example_value = data.get("example_value", "")
        self.example_kwargs = data.get("example_kwargs", {})
        self.example_env = data.get("example_env", {})
        self.description = data.get("description", "(no description)")
        self.form_definition = data.get("form_definition", "(no form definition)")
        self.init_done = False

    def __getattribute__(self, s):
        if not object.__getattribute__(self, "init_done") and s in ("check", "fix", "fixable"):
            object.__setattr__(self, "init_done", True)
            object.__getattribute__(self, "init")()
        return object.__getattribute__(self, s)

    def init(self):
        pass

    def test(self):
        self.__init__(**self.example_kwargs)
        self.prefix = "OSVC_COMP_CO_TEST"
        for k, v in self.example_env.items():
            self.set_env(k, v)
        self.set_env(self.prefix, self.example_value)
        return self.check()

    def info(self):
        def indent(text):
            lines = text.split("\n")
            return "\n".join(["    "+line for line in lines])
        s = ""
        s += "Description\n"
        s += "===========\n"
        s += "\n"
        s += indent(self.description)+"\n"
        s += "\n"
        s += "Example rule\n"
        s += "============\n"
        s += "\n::\n\n"
        s += indent(json.dumps(json.loads(self.example_value), indent=4, separators=(',', ': ')))+"\n"
        s += "\n"
        s += "Form definition\n"
        s += "===============\n"
        s += "\n::\n\n"
        s += indent(self.form_definition)+"\n"
        s += "\n"
        pinfo(s)

    def set_prefix(self, prefix):
        self.prefix = prefix.upper()

    def set_env(self, k, v):
        if sys.version_info[0] < 3:
            v = v.decode("utf-8")
        os.environ[k] = v

    def get_env(self, k):
        s = os.environ[k]
        if sys.version_info[0] < 3:
            s = s.encode("utf-8")
        return s

    def get_rules_raw(self):
        rules = []
        for k in [key for key in os.environ if key.startswith(self.prefix)]:
            s = self.subst(self.get_env(k))
            rules += [s]
        if len(rules) == 0:
            raise NotApplicable("no rules (%s)" % self.prefix)
        return rules

    def encode_data(self, data):
        if sys.version_info[0] > 2:
            return data
        if type(data) == dict:
            for k in data:
                if isinstance(data[k], (str, unicode)):
                    data[k] = data[k].encode("utf-8")
                elif isinstance(data[k], (list, dict)):
                    data[k] = self.encode_data(data[k])
        elif type(data) == list:
            for i, v in enumerate(data):
                if isinstance(v, (str, unicode)):
                    data[i] = v.encode("utf-8")
                elif isinstance(data[i], (list, dict)):
                    data[i] = self.encode_data(data[i])
        return data

    def get_rules(self):
        return [self.encode_data(v[1]) for v in self.get_rule_items()]

    def get_rule_items(self):
        rules = []
        for k in [key for key in os.environ if key.startswith(self.prefix)]:
            try:
                s = self.subst(self.get_env(k))
            except Exception as e:
                perror(k, e)
                continue
            try:
                data = json.loads(s)
            except ValueError:
                perror("failed to concatenate '%s=%s' to rules list" % (k, str(self.get_env(k))))
                continue
            if type(data) == list:
                for d in data:
                    rules += [(k, d)]
            else:
                rules += [(k, data)]
        if len(rules) == 0:
            raise NotApplicable("no rules (%s)" % self.prefix)
        return rules

    def subst(self, v):
        """
          A rule value can contain references to other rules as %%ENV:OTHER%%.
          This function substitutes these markers with the referenced rules values,
          which may themselves contain references. Hence the recursion.
        """
        max_recursion = 10

        if type(v) == list:
            l = []
            for _v in v:
                l.append(self.subst(_v))
            return l
        if type(v) != str and type(v) != unicode:
            return v

        p = re.compile('%%ENV:\w+%%', re.IGNORECASE)

        def _subst(v):
            matches = p.findall(v)
            if len(matches) == 0:
                raise EndRecursion
            for m in matches:
                s = m.strip("%").upper().replace('ENV:', '')
                if s in os.environ:
                    _v = self.get_env(s)
                elif 'OSVC_COMP_'+s in os.environ:
                    _v = self.get_env('OSVC_COMP_'+s)
                else:
                    _v = ""
                    raise NotApplicable("undefined substitution variable: %s" % s)
                v = v.replace(m, _v)
            return v

        for i in range(max_recursion):
            try:
                v = _subst(v)
            except EndRecursion:
                break

        p = re.compile('%%SAFE:\w+%%', re.IGNORECASE)

        def _subst_safe(v):
            matches = p.findall(v)
            if len(matches) == 0:
                raise EndRecursion
            for m in matches:
                s = m.strip("%").upper().replace('SAFE:', '')
                _v = self.collector_rest_get("/safe/%s/download" % s, load_json=False)
                v = v.replace(m, _v)
            return v

        for i in range(max_recursion):
            try:
                v = _subst_safe(v)
            except EndRecursion:
                break

        return v

    def collector_api(self):
        if hasattr(self, "collector_api_cache"):
            return self.collector_api_cache
        import platform
        sysname, nodename, x, x, machine, x = platform.uname()
        try:
            import ConfigParser
        except ImportError:
            import configparser as ConfigParser
        config = ConfigParser.RawConfigParser({})
        if os.path.realpath(__file__).startswith("/opt/opensvc"):
            config.read("/opt/opensvc/etc/node.conf")
        else:
            config.read("/etc/opensvc/node.conf")
        data = {}
        svcname = os.environ.get("OSVC_COMP_SERVICES_SVCNAME")
        if svcname:
            data["username"] = svcname+"@"+nodename
        else:
            data["username"] = nodename
        data["password"] = config.get("node", "uuid")
        data["url"] = config.get("node", "dbopensvc").replace("/feed/default/call/xmlrpc", "")
        data["url"] = data["url"].replace("/init/rest/api", "")
        data["url"] += "/init/rest/api"
        if not data["url"].startswith("http"):
            data["url"] = "https://%s" % data["url"]
        self.collector_api_cache = data
        return self.collector_api_cache

    def collector_url(self):
        api = self.collector_api()
        s = "%s:%s@" % (api["username"], api["password"])
        url = api["url"].replace("https://", "")
        url = url.replace("http://", "")
        url = "https://"+s+url
        return url

    def collector_request(self, path):
        api = self.collector_api()
        url = api["url"]
        request = Request(url+path)
        base64string = base64.encodestring('%s:%s' % (api["username"], api["password"])).replace('\n', '')
        request.add_header("Authorization", "Basic %s" % base64string)
        return request

    def collector_rest_get(self, path, load_json=True):
        api = self.collector_api()
        request = self.collector_request(path)
        if api["url"].startswith("https"):
            try:
                import ssl
                kwargs = {"context": ssl._create_unverified_context()}
            except:
                kwargs = {}
        else:
            raise ComplianceError("refuse to submit auth tokens through a non-encrypted transport")
        try:
            f = urlopen(request, **kwargs)
        except HTTPError as e:
            try:
                err = json.loads(e.read())["error"]
                e = ComplianceError(err)
            except:
                pass
            raise e
        if load_json:
            data = json.loads(f.read())
        else:
            data = f.read()
        f.close()
        return data

    def collector_rest_get_to_file(self, path, fpath):
        api = self.collector_api()
        request = self.collector_request(path)
        if api["url"].startswith("https"):
            try:
                import ssl
                kwargs = {"context": ssl._create_unverified_context()}
            except:
                kwargs = {}
        else:
            raise ComplianceError("refuse to submit auth tokens through a non-encrypted transport")
        try:
            f = urlopen(request, **kwargs)
        except HTTPError as e:
            try:
                err = json.loads(e.read())["error"]
                e = ComplianceError(err)
            except:
                pass
            raise e
        with open(fpath, 'wb') as df:
            for chunk in iter(lambda: f.read(4096), b""):
                df.write(chunk)
        f.close()

    def collector_safe_uri_to_uuid(self, uuid):
        if uuid.startswith("safe://"):
            uuid = uuid.replace("safe://", "")
        try:
            int(uuid)
            isint = True
        except ValueError:
            isint = False
        if not uuid.startswith("safe") and not isint:
            raise ComplianceError("malformed safe file uri: %s" % uuid)
        return uuid

    def collector_safe_file_download(self, uuid, fpath):
        uuid = self.collector_safe_uri_to_uuid(uuid)
        self.collector_rest_get_to_file("/safe/" + uuid + "/download", fpath)

    def collector_safe_file_get_meta(self, uuid):
        uuid = self.collector_safe_uri_to_uuid(uuid)
        data = self.collector_rest_get("/safe/" + uuid)
        if len(data["data"]) == 0:
            raise ComplianceError(uuid + ": metadata not found")
        return data["data"][0]

    def urlretrieve(self, url, fpath):
        request = Request(url)
        kwargs = {}
        if sys.hexversion >= 0x02070900:
            import ssl
            kwargs["context"] = ssl._create_unverified_context()
        f = urlopen(request, **kwargs)
        with open(fpath, 'wb') as df:
            for chunk in iter(lambda: f.read(4096), b""):
                df.write(chunk)

    def md5(self, fpath):
        import hashlib
        hash = hashlib.md5()
        with open(fpath, 'rb') as f:
            for chunk in iter(lambda: f.read(4096), b""):
                hash.update(chunk)
        return hash.hexdigest()

    def backup(self, path):
        import shutil
        if not os.path.exists(path):
            return
        session_uuid = os.environ.get("OSVC_SESSION_UUID")
        pathvar = os.environ.get("OSVC_PATH_VAR")
        if session_uuid is None or pathvar is None:
            return
        backup_base_d = os.path.join(pathvar, "compliance_backup", session_uuid)
        backup_f = os.path.join(backup_base_d, path.lstrip(os.sep))
        if os.path.exists(backup_f):
            return
        backup_d = os.path.dirname(backup_f)
        if not os.path.exists(backup_d):
            try:
                os.makedirs(backup_d)
            except OSError as exc:
                perror("failed to backup %s: create dir %s" % (path, backup_d))
                raise ComplianceError()
        try:
            shutil.copy(path, backup_f)
        except Exception:
            perror("failed to backup %s: copy to %s" % (path, backup_f))
            raise ComplianceError()
        self.remove_old_backups()

    def restore(self, path):
        import shutil
        if not os.path.exists(path):
            return
        session_uuid = os.environ.get("OSVC_SESSION_UUID")
        if session_uuid is None:
            return
        backup_base_d = os.path.join(pathvar, "compliance_backup", session_uuid)
        backup_f = os.path.join(backup_base_d, path)
        if not backup_f:
            perror("failed to restore %s: no backup" % path)
            raise ComplianceError()
        try:
            shutil.copy(backup, path)
            pinfo("%s restored" % path)
        except Exception:
            perror("failed to restore %s" % path)
            raise ComplianceError()

    def remove_old_backups(self):
        import glob
        import time
        import shutil
        threshold = time.time() - 7 * 24 * 60 * 60
        pathvar = os.environ.get("OSVC_PATH_VAR")
        for path in glob.glob(os.path.join(pathvar, "compliance_backup", "*-*-*-*-*")):
            mtime = os.stat(path).st_mtime
            if mtime < threshold:
                shutil.rmtree(path)


    #
    # Placeholders, to override in child class
    #
    def check(self):
        return RET_NA

    def fixable(self):
        return RET_NA

    def fix(self):
        return RET_NA



def main(co):
    syntax =  "syntax:\n"
    syntax += """ %s <ENV VARS PREFIX> check|fix|fixable\n"""%sys.argv[0]
    syntax += """ %s test|info"""%sys.argv[0]

    try:
        o = co()
    except NotApplicable as e:
        pinfo(e)
        sys.exit(RET_NA)
    if o.extra_syntax_parms:
        syntax += " "+o.extra_syntax_parms

    if len(sys.argv) == 2:
        if sys.argv[1] == 'test':
            try:
                RET = o.test()
                sys.exit(RET)
            except ComplianceError as e:
                perror(e)
                sys.exit(RET_ERR)
            except NotApplicable:
                sys.exit(RET_NA)
        elif sys.argv[1] == 'info':
            o.info()
            sys.exit(0)

    if len(sys.argv) < 3:
        perror(syntax)
        sys.exit(RET_ERR)

    argv = [sys.argv[1]]
    if len(sys.argv) > 3:
        argv += sys.argv[3:]
    o.__init__(*argv)
    try:
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
    except ComplianceError as e:
        perror(e)
        sys.exit(RET_ERR)
    except NotApplicable as e:
        pinfo(e)
        sys.exit(RET_NA)
    except:
        import traceback
        traceback.print_exc()
        sys.exit(RET_ERR)

    sys.exit(RET)

if __name__ == "__main__":
    perror("this file is for import into compliance objects")
