#!/usr/bin/env python

from __future__ import print_function
import os
import sys

def is_exe(fpath):
    """Returns True if file path is executable, False otherwize
    does not follow symlink
    """
    return os.path.exists(fpath) and os.access(fpath, os.X_OK)

def which(program):
    """Returns True if program is in PATH and executable, False
    otherwize
    """
    fpath, fname = os.path.split(program)
    if fpath and is_exe(program):
        return program
    for path in os.environ["PATH"].split(os.pathsep):
        exe_file = os.path.join(path, program)
        if is_exe(exe_file):
            return exe_file
    return None

if __name__ == "__main__":
    print("this file is for import into compliance objects", file=sys.stderr)

