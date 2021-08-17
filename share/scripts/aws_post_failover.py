#!/usr/bin/env python3
# aws private vip failover

import boto3
from botocore.exceptions import ClientError
from subprocess import Popen, PIPE
from shlex import split
import sys
import json
import logging

logging.basicConfig(level=logging.INFO,
                    format='%(asctime)s %(filename)s[line:%(lineno)d] %(levelname)s %(message)s',
                    datefmt='%Y-%m-%d %H:%M:%S',
                    filename='/data/replication-manager/log/awsviphelper.log')


class AWSVIPHelper(object):
    '''
    access_key accesskeyid
    access_secrect accesskeysecret
    region_name  https://boto3.amazonaws.com/v1/documentation/api/latest/guide/configuration.html
    eni_id
    '''
    def __init__(self, access_key, access_secrect, region_name, eni_id, vip):
        self._access_key = access_key
        self._access_secrect = access_secrect
        self._region_name = region_name
        self._eni_id = eni_id
        self._vip = vip
        self._clt = boto3.client(
            'ec2',
            aws_access_key_id=self._access_key,
            aws_secret_access_key=self._access_secrect,
            region_name=self._region_name
        )
        self._errmsg = None

    def _assign_vip(self):
        try:
            response_str = self._clt.assign_private_ip_addresses(
                AllowReassignment=True,
                NetworkInterfaceId=self._eni_id,
                PrivateIpAddresses=[self._vip]
            )
            logging.info(response_str)
            #response_detail = json.loads(response_str)
            #logging.info(response_detail)
            return True
        except ClientError as e:
            logging.error(e)
            return False
        except Exception as e:
            logging.error(e)
            return False

    def _unassign_vip(self):
        try:
            response_str = self._clt.unassign_private_ip_addresses(
                NetworkInterfaceId=self._eni_id,
                PrivateIpAddresses=[self._vip]
            )
            logging.info(response_str)
            #response_detail = json.loads(response_str)
            #logging.info(response_detail)
            return True
        except ClientError as e:
            logging.error(e)
            return False
        except Exception as e:
            logging.error(e)
            return False

def execute_commond_get_stdout(commond):
    commond_list = split(commond)
    p = Popen(commond_list, stdout=PIPE, stderr=PIPE)
    stdout, stderr = p.communicate()
    if p.returncode !=0:
        return stderr.decode(errors='ignore')
    elif len(stderr) != 0:
        return stderr.decode(errors='ignore')
    return stdout.decode(errors='ignore')

if __name__ == '__main__':
    res = True
    eni_id = ""
    old_master_host = ""
    new_master_host = ""
    try:
        if len(sys.argv) != 5:
            logging.error("params error:{0}".format(" ".join(sys.argv)))
            res = False
        else:
            vip = "172.31.26.1"
            access_key = ""
            access_secrect = ""
            region_name = "us-west-1"
            host_enis = {"172.31.26.243":"awshctest@eni-028da8368116483f7","172.31.17.45":"awshctest02@eni-074404fa47c806ee0"}
            old_master_ip = str(sys.argv[1])
            new_master_ip = str(sys.argv[2])
            if new_master_ip in host_enis:
                hosteni = host_enis[new_master_ip]
                new_master_host = hosteni.split('@')[0]
                eni_id = hosteni.split('@')[1]
            if old_master_ip in host_enis:
                old_master_host = host_enis[old_master_ip].split('@')[0]
            if eni_id == "":
                logging.error("ip:{0} cat not get eni_id".format(new_master_ip))
                res = False
            else:
                helper = AWSVIPHelper(access_key, access_secrect, region_name, eni_id, vip)
                res = helper._assign_vip()
        title = "vip failover"
        content = "old master：{0}\nnew master：{1}\nvip failover result：{2}".format(old_master_host, new_master_host, 'success' if res else 'fail')
        cmd = "/data/replication-manager/scripts/alert.py '{0}' '{1}'".format(title, content)
        execute_commond_get_stdout(cmd)
    except Exception as e:
        logging.error(e)
