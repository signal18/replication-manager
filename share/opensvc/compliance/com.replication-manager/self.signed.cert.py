#!/usr/bin/env python
data = {
  "default_prefix": "OSVC_COMP_CERT_",
  "example_value": """ 
{
    "CN": "%%ENV:SERVICES_SVCNAME%%",
    "crt": "/srv/%%ENV:SERVICES_SVCNAME%%/data/nginx/conf/ssl/server.crt",
    "key": "/srv/%%ENV:SERVICES_SVCNAME%%/data/nginx/conf/ssl/server.key",
    "bits": 2048,
    "C": "FR",
    "ST": "Ile de France",
    "L": "Paris",
    "O": "OpenSVC",
    "OU": "Lab",
    "emailAddress": "support@opensvc.com",
    "alt_names": [
        {
            "dns": ""
        }
    ]
}
""",
  "description": """* Check the existance of a key/crt pair
* Create the key/crt pair
""",
  "form_definition": """
Desc: |
  Describe a self-signed certificate
Css: comp48

Outputs:
  -
    Dest: compliance variable
    Type: json
    Format: dict
    Class: authkey

Inputs:
  -
    Id: CN
    Label: Common name
    DisplayModeLabel: cn
    LabelCss: loc
    Mandatory: Yes
    Type: string
  -
    Id: crt
    Label: Cert path
    DisplayModeLabel: crt
    LabelCss: key
    Mandatory: Yes
    Type: string
    Help: Where to install the generated certificate
  -
    Id: key
    Label: Key path
    DisplayModeLabel: key
    LabelCss: key
    Mandatory: Yes
    Type: string
    Help: Where to install the generated key
  -
    Id: bits
    Label: Bits
    DisplayModeLabel: bits
    LabelCss: key
    Mandatory: Yes
    Type: integer
    Default: 2048
    Help: Defines the key length in bits
  -
    Id: C
    Label: Country name
    DisplayModeLabel: country
    LabelCss: loc
    Mandatory: Yes
    Default: FR
    Type: string
  -
    Id: ST
    Label: State or Province
    DisplayModeLabel: state
    LabelCss: loc
    Mandatory: Yes
    Default: Ile de France
    Type: string
  -
    Id: L
    Label: Locality name
    DisplayModeLabel: locality
    LabelCss: loc
    Mandatory: Yes
    Default: Paris
    Type: string
  -
    Id: O
    Label: Organization name
    DisplayModeLabel: org
    LabelCss: loc
    Mandatory: Yes
    Default: OpenSVC
    Type: string
  -
    Id: OU
    Label: Organization unit
    DisplayModeLabel: org unit
    LabelCss: loc
    Mandatory: Yes
    Default: IT
    Type: string
  -
    Id: emailAddress
    Label: Email address
    DisplayModeLabel: email
    LabelCss: loc
    Mandatory: Yes
    Default: admin@opensvc.com
    Type: string
  -
    Id: alt_names
    Label: Alternate names
    DisplayModeLabel: alt names
    LabelCss: loc
    Type: form
    Form: self.signed.cert.alt_names
    Default: []


Subform:

Desc: |
  Subform for the self.signed.cert form.
Css: comp48

Outputs:
  -
    Type: json
    Format: list of dict

Inputs:
  -
    Id: dns
    Label: DNS
    DisplayModeLabel: dns
    LabelCss: loc
    Type: string
    Help: An alternate service name

    """
}

import os
import sys

sys.path.append(os.path.dirname(__file__))

from comp import *
from utilities import which
from subprocess import *

class CompSelfSignedCert(CompObject):
    def __init__(self, prefix='OSVC_COMP_CERT_'):
        CompObject.__init__(self, prefix=prefix, data=data)

    def init(self):
        self.rules = self.get_rules()
        if which("openssl") is None:
            raise NotApplicable("openssl command not found")

    def check(self):
        r = 0
        for rule in self.rules:
            r |= self.check_rule(rule)
        return r

    def fix(self):
        r = 0
        for rule in self.rules:
            r |= self.fix_rule(rule)
        return r

    def check_rule(self, rule):
        r = RET_OK
        if not os.path.exists(rule["key"]):
            perror("key %s does not exist" % rule["key"])
            r = RET_ERR
        else:
            pinfo("key %s exists" % rule["key"])
        if not os.path.exists(rule["crt"]):
            perror("crt %s does not exist" % rule["crt"])
            r = RET_ERR
        else:
            pinfo("crt %s exists" % rule["crt"])
        return r

    def fix_rule(self, rule):
        if os.path.exists(rule["key"]) and os.path.exists(rule["crt"]):
            return RET_OK
        for k in ("key", "crt"):
            d = os.path.dirname(rule[k])
            if not os.path.isdir(d):
                if os.path.exists(d):
                    perror("%s exists but is not a directory" % d)
                    return RET_ERR
                else:
                    pinfo("mkdir -p %s" %d)
                    os.makedirs(d)
        l = [""]
        for k in ["C", "ST", "L", "O", "OU", "CN", "emailAddress"]:
            l.append(k+"="+rule[k])
        if "alt_names" in rule and len(rule["alt_names"]) > 0:
            dns = []
            for i, d in enumerate(rule["alt_names"]):
                dns.append("DNS.%d=%s" % (i+1, d["DNS"]))
            l.append("subjectAltName="+",".join(dns))
        l.append("")
        cmd = ["openssl", "req", "-x509", "-nodes",
               "-newkey", "rsa:%d" % rule["bits"],
               "-keyout", rule["key"],
               "-out", rule["crt"],
               "-days", "XXX",
               "-subj", "%s" % "/".join(l)]
        pinfo(" ".join(cmd))
        p = Popen(cmd, stdout=PIPE, stderr=PIPE)
        out, err = p.communicate()
        if p.returncode != 0:
            if len(out) > 0:
                pinfo(out)
            if len(err) > 0:
                perror(err) 
            return RET_ERR
        return RET_OK

if __name__ == "__main__":
    main(CompSelfSignedCert)

