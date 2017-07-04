#!/usr/bin/env python

import os
import sys
import datetime
import shutil

sys.path.append(os.path.dirname(__file__))

from comp import *

class ParserError(Exception):
    pass

class Parser(object):
    def __init__(self, path, section_markers=None):
        self.path = path
        self.data = {}
        self.changed = False
        self.nocf = False
        self.keys = []
        self.sections = {}
        self.section_names = []
        self.lastkey = '__lastkey__'
        self.comments = {self.lastkey: []}
        if section_markers:
            self.section_markers = section_markers
        else:
            self.section_markers = ["Match"]
        self.load()
        self.bkp = path + '.' + str(datetime.datetime.now())

    def __str__(self):
        s = ""
        for k in self.keys:
            if k in self.comments:
                s += '\n'.join(self.comments[k]) + '\n'
            s += '\n'.join([k + " " + str(v) for v in self.data[k]]) + '\n'
        if len(self.comments[self.lastkey]) > 0:
            s += '\n'.join(self.comments[self.lastkey])
        for section, data in self.sections.items():
            s += section + '\n'
            for k in data["keys"]:
                for v in data["data"][k]:
                    s += "\t" + k + " " + str(v) + '\n'
        return s

    def truncate(self, key, max):
        if key not in self.data:
            return
        n = len(self.data[key])
        if n <= max:
            return
        self.data[key] = self.data[key][:max]
        self.changed = True

    def set(self, key, value, instance=0):
        if key not in self.data:
            self.data[key] = [value]
            self.keys.append(key)
        elif instance >= len(self.data[key]):
            extra = instance + 1 - len(self.data[key])
            for i in range(len(self.data[key]), instance-1):
                self.data[key].append(None)
            self.data[key].append(value)
        else:
            self.data[key].insert(instance, value)
        self.changed = True

    def unset(self, key, value=None):
        if key in self.data:
            if value is not None and value.strip() != "":
                self.data[key].remove(value)
            else:
                self.data[key] = []
            if len(self.data[key]) == 0:
                del(self.data[key])
                if key in self.keys:
                    self.keys.remove(key)
        self.changed = True

    def get(self, key, instance=0):
        if key not in self.data:
            return
        if instance is None:
            return self.data[key]
        if instance < len(self.data[key]):
            return self.data[key][instance]
        return

    def load(self):
        if not os.path.exists(self.path):
            raise ParserError("%s does not exist"%self.path)
            self.nocf = True
            return
        with open(self.path, 'r') as f:
            buff = f.read()
        self.parse(buff)

    def backup(self):
        if self.nocf:
            return
        try:
            shutil.copy(self.path, self.bkp)
        except Exception as e:
            perror(e)
            raise ParserError("failed to backup %s"%self.path)
        pinfo("%s backup up as %s" % (self.path, self.bkp))

    def restore(self):
        if self.nocf:
            return
        try:
            shutil.copy(self.bkp, self.path)
        except:
            raise ParserError("failed to restore %s"%self.path)
        pinfo("%s restored from %s" % (self.path, self.bkp))


    def write(self):
        self.backup()
        try:
            with open(self.path, 'w') as f:
                f.write(str(self))
            pinfo("%s rewritten"%self.path)
        except Exception as e:
            perror(e)
            self.restore()
            raise ParserError()

    def parse(self, buff):
        section = None

        for line in buff.split("\n"):
            line = line.strip()

            # store comment line and continue
            if line.startswith('#') or len(line) == 0: 
                self.comments[self.lastkey].append(line)
                continue

            # strip end-of-line comment
            try:
                i = line.index('#')
                line = line[:i]
                line = line.strip()
            except ValueError:
                pass

            # discard empty line
            if len(line) == 0:
                continue

            l = line.split()
            if len(l) < 2:
                 continue
            key = l[0]
            value = line[len(key):].strip()

            if key not in self.comments:
                self.comments[key] = self.comments[self.lastkey]
            else:
                self.comments[key] += self.comments[self.lastkey]
            self.comments[self.lastkey] = []

            try:
                value = int(value)
            except:
                pass

            if key in self.section_markers:
                section = key + " " + value
                if section not in self.sections:
                    self.sections[section] = {"keys": [], "data": {}}
                    self.section_names.append(section)
                continue

            if section:
                if key not in self.sections[section]["keys"]:
                    self.sections[section]["keys"].append(key)
                if key not in self.sections[section]["data"]:
                    self.sections[section]["data"][key] = []
                self.sections[section]["data"][key].append(value)
            else:
                if key not in self.keys:
                    self.keys.append(key)
                if key not in self.data:
                    self.data[key] = []
                self.data[key].append(value)

if __name__ == "__main__":
    if len(sys.argv) != 2:
        perror("wrong number of arguments")
        sys.exit(1)
    o = Parser(sys.argv[1])
    o.get("Subsystem")
    o.set("Subsystem", "foo")
    o.unset("PermitRootLogin")
    o.backup()
    pinfo(o)

