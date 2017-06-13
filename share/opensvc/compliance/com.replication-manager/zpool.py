#!/usr/bin/env python
data = {
  "default_prefix": "OSVC_COMP_ZPOOL_",
  "example_value": """ 
[
 {
  "name": "rpool",
  "prop": "failmode",
  "op": "=",
  "value": "continue"
 },
 {
  "name": "rpool",
  "prop": "dedupditto",
  "op": "<",
  "value": 1
 },
 {
  "name": "rpool",
  "prop": "dedupditto",
  "op": ">",
  "value": 0
 },
 {
  "name": "rpool",
  "prop": "dedupditto",
  "op": "<=",
  "value": 1
 },
 {
  "name": "rpool",
  "prop": "dedupditto",
  "op": ">=",
  "value": 1
 }
]
""",
  "description": """* Check the properties values against their target and operator
* The collector provides the format with wildcards.
* The module replace the wildcards with contextual values.
* In the 'fix' the zpool property is set.
""",
  "form_definition": """
Desc: |
  A rule to set a list of zpool properties.
Css: comp48

Outputs:
  -
    Dest: compliance variable
    Type: json
    Format: list of dict
    Class: zpool

Inputs:
  -
    Id: name
    Label: Pool Name
    DisplayModeLabel: poolname
    LabelCss: hd16
    Mandatory: Yes
    Type: string
    Help: The zpool name whose property to check.
  -
    Id: prop
    Label: Property
    DisplayModeLabel: property
    LabelCss: action16
    Mandatory: Yes
    Type: string
    Help: The zpool property to check.
    Candidates:
      - readonly
      - autoexpand
      - autoreplace
      - bootfs
      - cachefile
      - dedupditto
      - delegation
      - failmode
      - listshares
      - listsnapshots
      - version

  -
    Id: op_s
    Key: op
    Label: Comparison operator
    DisplayModeLabel: op
    LabelCss: action16
    Type: info
    Default: "="
    ReadOnly: yes
    Help: The comparison operator to use to check the property current value.
    Condition: "#prop IN readonly,autoexpand,autoreplace,bootfs,cachefile,delegation,failmode,listshares,listsnapshots"
  -
    Id: op_n
    Key: op
    Label: Comparison operator
    DisplayModeLabel: op
    LabelCss: action16
    Mandatory: Yes
    Type: string
    Default: "="
    StrictCandidates: yes
    Candidates:
      - "="
      - ">"
      - ">="
      - "<"
      - "<="
    Help: The comparison operator to use to check the property current value.
    Condition: "#prop IN version,dedupditto"

  -
    Id: value_readonly
    Key: value
    Label: Value
    DisplayModeLabel: value
    LabelCss: action16
    Mandatory: Yes
    Type: string
    Help: The zpool property target value.
    Condition: "#prop == readonly"
    StrictCandidates: yes
    Candidates:
      - "on"
      - "off"
  -
    Id: value_autoexpand
    Key: value
    Label: Value
    DisplayModeLabel: value
    LabelCss: action16
    Mandatory: Yes
    Type: string
    Help: The zpool property target value.
    Condition: "#prop == autoexpand"
    StrictCandidates: yes
    Candidates:
      - "on"
      - "off"
  -
    Id: value_autoreplace
    Key: value
    Label: Value
    DisplayModeLabel: value
    LabelCss: action16
    Mandatory: Yes
    Type: string
    Help: The zpool property target value.
    Condition: "#prop == autoreplace"
    StrictCandidates: yes
    Candidates:
      - "on"
      - "off"
  -
    Id: value_delegation
    Key: value
    Label: Value
    DisplayModeLabel: value
    LabelCss: action16
    Mandatory: Yes
    Type: string
    Help: The zpool property target value.
    Condition: "#prop == delegation"
    StrictCandidates: yes
    Candidates:
      - "on"
      - "off"
  -
    Id: value_listshares
    Key: value
    Label: Value
    DisplayModeLabel: value
    LabelCss: action16
    Mandatory: Yes
    Type: string
    Help: The zpool property target value.
    Condition: "#prop == listshares"
    StrictCandidates: yes
    Candidates:
      - "on"
      - "off"
  -
    Id: value_listsnapshots
    Key: value
    Label: Value
    DisplayModeLabel: value
    LabelCss: action16
    Mandatory: Yes
    Type: string
    Help: The zpool property target value.
    Condition: "#prop == listsnapshots"
    StrictCandidates: yes
    Candidates:
      - "on"
      - "off"
  -
    Id: value_failmode
    Key: value
    Label: Value
    DisplayModeLabel: value
    LabelCss: action16
    Mandatory: Yes
    Type: string
    Help: The zpool property target value.
    Condition: "#prop == failmode"
    StrictCandidates: yes
    Candidates:
      - "continue"
      - "wait"
      - "panic"
  -
    Id: value_bootfs
    Key: value
    Label: Value
    DisplayModeLabel: value
    LabelCss: action16
    Mandatory: Yes
    Type: string
    Help: The zpool property target value.
    Condition: "#prop == bootfs"
  -
    Id: value_cachefile
    Key: value
    Label: Value
    DisplayModeLabel: value
    LabelCss: action16
    Mandatory: Yes
    Type: string
    Help: The zpool property target value.
    Condition: "#prop == cachefile"
  -
    Id: value_dedupditto
    Key: value
    Label: Value
    DisplayModeLabel: value
    LabelCss: action16
    Mandatory: Yes
    Type: integer
    Help: The zpool property target value.
    Condition: "#prop == dedupditto"
  -
    Id: value_version
    Key: value
    Label: Value
    DisplayModeLabel: value
    LabelCss: action16
    Mandatory: Yes
    Type: integer
    Help: The zpool property target value.
    Condition: "#prop == version"
"""
}

import os
import sys

sys.path.append(os.path.dirname(__file__))

from zprop import *

class CompZpool(CompZprop):
    def __init__(self, prefix='OSVC_COMP_ZPOOL_'):
        CompObject.__init__(self, prefix=prefix, data=data)
        self.zbin = "zpool"

if __name__ == "__main__":
    main(CompZpool)

