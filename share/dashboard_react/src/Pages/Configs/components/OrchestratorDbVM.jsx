import { VStack } from '@chakra-ui/react'
import React, { useState, useEffect } from 'react'

import Dropdown from '../../../components/Dropdown'
import TextForm from '../../../components/TextForm'
import TableType2 from '../../../components/TableType2'
import parentStyles from '../styles.module.scss'
import { useDispatch, useSelector } from 'react-redux'
import { setSetting, switchSetting } from '../../../redux/settingsSlice'
import { getKubeStorageClasses } from '../../../redux/clusterSlice'
import { convertObjectToArrayForDropdown } from '../../../utility/common'
import RMSwitch from '../../../components/RMSwitch'

// the configurator's Databases / Proxies tabs (#2268): scope 'proxy' keeps the proxy rows, 'db' the
// others (the database rows and those both share); no scope keeps every row
const PROXY_ROWS = ['Proxy Start Timeout', 'Proxy VM', 'Kubernetes Proxy Storage Class']
const scopedRows = (rows, scope) =>
  !scope ? rows : rows.filter((row) => (scope === 'proxy') === PROXY_ROWS.includes(row.key))

function OrchestratorDbVM({ selectedCluster, user, scope }) {
  const dispatch = useDispatch()
  const {
    globalClusters: { monitor }
  } = useSelector((state) => state)
  const kubeStorageClasses = useSelector((state) => state.cluster?.kubeStorageClasses || [])
  const [serviceVMs, setServiceVMs] = useState([])

  useEffect(() => {
    if (monitor?.serviceVM) {
      setServiceVMs(convertObjectToArrayForDropdown(monitor.serviceVM))
    }
  }, [monitor?.serviceVM])

  useEffect(() => {
    if (selectedCluster?.name && selectedCluster?.config?.provOrchestrator === 'kube') {
      dispatch(getKubeStorageClasses({ clusterName: selectedCluster.name }))
    }
  }, [selectedCluster?.name, selectedCluster?.config?.provOrchestrator])

  const dataObject = [
    {
      clusterSetting: 'prov-db-start-timeout',
      key: 'Database Start Timeout',
      value: (
        <TextForm
          value={selectedCluster?.config?.provDbStartTimeout}
          placeholder='2m'
          confirmTitle="Database Start Timeout Change"
          confirmBody='Start and image pull timeout of the database containers in the orchestrator (the orchestrator default is 5s, too short for an image pull after a purge). Change "prov-db-start-timeout" to: '
          onSave={(value) => dispatch(setSetting({ clusterName: selectedCluster?.name, setting: 'prov-db-start-timeout', value: value }))}
        />
      )
    },
    {
      clusterSetting: 'prov-proxy-start-timeout',
      key: 'Proxy Start Timeout',
      value: (
        <TextForm
          value={selectedCluster?.config?.provProxyStartTimeout}
          placeholder='2m'
          confirmTitle="Proxy Start Timeout Change"
          confirmBody='Start and image pull timeout of the proxy containers in the orchestrator. Change "prov-proxy-start-timeout" to: '
          onSave={(value) => dispatch(setSetting({ clusterName: selectedCluster?.name, setting: 'prov-proxy-start-timeout', value: value }))}
        />
      )
    },
    {
      clusterSetting: 'prov-db-service-type',
      key: 'Database VM',
      value: (
        <Dropdown
          className={parentStyles.dropdown}
          options={serviceVMs}
          selectedValue={selectedCluster?.config?.provDbServiceType}
          confirmTitle={`Confirm change database VM type to `}
          onChange={(value) => {
            dispatch(
              setSetting({
                clusterName: selectedCluster?.name,
                setting: 'prov-db-service-type',
                value: value
              })
            )
          }}
        />
      )
    },
    {
      clusterSetting: 'prov-proxy-service-type',
      key: 'Proxy VM',
      value: (
        <Dropdown
          className={parentStyles.dropdown}
          options={serviceVMs}
          selectedValue={selectedCluster?.config?.provProxyServiceType}
          confirmTitle={`Confirm change proxy VM type to `}
          onChange={(value) => {
            dispatch(
              setSetting({
                clusterName: selectedCluster?.name,
                setting: 'prov-proxy-service-type',
                value: value
              })
            )
          }}
        />
      )
    },
    {
      clusterSetting: 'prov-net-cni',
      key: 'Provisioning CNI',
      value: (
        <RMSwitch
          isChecked={selectedCluster?.config?.provNetCni}
          isDisabled={user?.grants['cluster-settings'] == false}
          confirmTitle={'Confirm switch settings for prov-net-cni?'}
          onChange={() => dispatch(switchSetting({ clusterName: selectedCluster?.name, setting: 'prov-net-cni' }))}
        />
      )
    },
    selectedCluster?.config?.provOrchestrator === 'kube' && {
      clusterSetting: 'prov-kube-storage-class',
      key: 'Kubernetes Storage Class',
      value: (
        <Dropdown
          className={parentStyles.dropdown}
          options={kubeStorageClasses}
          selectedValue={selectedCluster?.config?.provKubeStorageClass}
          confirmTitle={`Confirm change Kubernetes storage class to `}
          onChange={(value) => {
            dispatch(
              setSetting({
                clusterName: selectedCluster?.name,
                setting: 'prov-kube-storage-class',
                value: value
              })
            )
          }}
        />
      )
    },
    selectedCluster?.config?.provOrchestrator === 'kube' && {
      clusterSetting: 'prov-kube-proxy-storage-class',
      key: 'Kubernetes Proxy Storage Class',
      value: (
        <Dropdown
          className={parentStyles.dropdown}
          options={kubeStorageClasses}
          selectedValue={selectedCluster?.config?.provKubeProxyStorageClass}
          confirmTitle={`Confirm change Kubernetes proxy storage class to `}
          onChange={(value) => {
            dispatch(
              setSetting({
                clusterName: selectedCluster?.name,
                setting: 'prov-kube-proxy-storage-class',
                value: value
              })
            )
          }}
        />
      )
    },
    selectedCluster?.config?.provOrchestrator === 'kube' && {
      // The layout of a database PVC that does not exist yet (#2395); an existing PVC keeps the layout its annotation says.
      clusterSetting: 'prov-kube-pvc-layout',
      key: 'Kubernetes Database PVC Layout (new volumes)',
      value: (
        <Dropdown
          className={parentStyles.dropdown}
          options={[
            { name: 'service-dirs-v1 (data, etc/mysql, init: the OpenSVC service directory)', value: 'service-dirs-v1' },
            { name: 'legacy (datadir at the PVC root, conf.d only)', value: 'legacy' }
          ]}
          selectedValue={selectedCluster?.config?.provKubePvcLayout || 'service-dirs-v1'}
          confirmTitle={`Confirm change Kubernetes database PVC layout for new volumes to `}
          onChange={(value) => {
            dispatch(
              setSetting({
                clusterName: selectedCluster?.name,
                setting: 'prov-kube-pvc-layout',
                value: value
              })
            )
          }}
        />
      )
    },
    selectedCluster?.config?.provOrchestrator === 'kube' && {
      clusterSetting: 'prov-kube-image-force-pull',
      key: 'Kubernetes Force Image Pull',
      value: (
        <RMSwitch
          isChecked={selectedCluster?.config?.provKubeImageForcePull}
          isDisabled={user?.grants['cluster-settings'] == false}
          confirmTitle={'Confirm switch settings for prov-kube-image-force-pull?'}
          onChange={() =>
            dispatch(switchSetting({ clusterName: selectedCluster?.name, setting: 'prov-kube-image-force-pull' }))
          }
        />
      )
    },
    ...(selectedCluster?.config?.provOrchestrator === 'proxmox'
      ? [
          {
            clusterSetting: 'proxmox-api-url',
            key: 'Proxmox API URL',
            value: (
              <TextForm
                value={selectedCluster?.config?.proxmoxAPIURL}
                placeholder='https://pve.example:8006'
                confirmTitle='Proxmox API URL Change'
                confirmBody='The Proxmox VE API endpoint of the hosts of this cluster. Change "proxmox-api-url" to: '
                onSave={(value) => dispatch(setSetting({ clusterName: selectedCluster?.name, setting: 'proxmox-api-url', value: btoa(value) }))}
              />
            )
          },
          {
            clusterSetting: 'proxmox-api-token',
            key: 'Proxmox API Token',
            value: (
              <TextForm
                type='password'
                value={selectedCluster?.config?.proxmoxAPIToken || ''}
                placeholder='user@realm!tokenid=secret'
                confirmTitle='Proxmox API Token Change'
                confirmBody='The API token, encrypted in the configuration and never logged. Change "proxmox-api-token"'
                onSave={(value) => dispatch(setSetting({ clusterName: selectedCluster?.name, setting: 'proxmox-api-token', value: btoa(value) }))}
              />
            )
          },
          {
            clusterSetting: 'proxmox-api-insecure',
            key: 'Proxmox API Accept Any Certificate',
            value: (
              <RMSwitch
                isChecked={selectedCluster?.config?.proxmoxAPIInsecure}
                isDisabled={user?.grants['cluster-settings'] == false}
                confirmTitle={'Confirm switch settings for proxmox-api-insecure (a lab only)?'}
                onChange={() => dispatch(switchSetting({ clusterName: selectedCluster?.name, setting: 'proxmox-api-insecure' }))}
              />
            )
          },
          {
            clusterSetting: 'prov-db-host-vmids',
            key: 'Database Host VMs (host=node/vmid)',
            value: (
              <TextForm
                value={selectedCluster?.config?.provDbHostVmids}
                placeholder='db1=pve1/101,db2=pve2/102'
                confirmTitle='Database Host VMs Change'
                confirmBody='The VM under each database server, host=node/vmid pairs comma separated. Change "prov-db-host-vmids" to: '
                onSave={(value) => dispatch(setSetting({ clusterName: selectedCluster?.name, setting: 'prov-db-host-vmids', value: btoa(value) }))}
              />
            )
          },
          {
            clusterSetting: 'prov-db-rolling-upgrade-host',
            key: 'Rolling Upgrade Reboots The Host',
            value: (
              <RMSwitch
                isChecked={selectedCluster?.config?.provDbRollingUpgradeHost}
                isDisabled={user?.grants['cluster-settings'] == false}
                confirmTitle={'Confirm switch settings for prov-db-rolling-upgrade-host?'}
                onChange={() => dispatch(switchSetting({ clusterName: selectedCluster?.name, setting: 'prov-db-rolling-upgrade-host' }))}
              />
            )
          },
          {
            clusterSetting: 'prov-db-host-upgrade-script',
            key: 'Host Upgrade Script (run in the guest before the reboot)',
            value: (
              <TextForm
                value={selectedCluster?.config?.provDbHostUpgradeScript}
                placeholder='/etc/replication-manager/host-upgrade.sh'
                confirmTitle='Host Upgrade Script Change'
                confirmBody='A local script run over ssh in the guest before its reboot in the rolling upgrade; empty: reboot only. Change "prov-db-host-upgrade-script" to: '
                onSave={(value) => dispatch(setSetting({ clusterName: selectedCluster?.name, setting: 'prov-db-host-upgrade-script', value: btoa(value) }))}
              />
            )
          },
          {
            clusterSetting: 'prov-db-host-reboot-timeout',
            key: 'Host Reboot Timeout (s)',
            value: (
              <TextForm
                value={selectedCluster?.config?.provDbHostRebootTimeout}
                placeholder='600'
                confirmTitle='Host Reboot Timeout Change'
                confirmBody='Seconds for a host action and for a rebooted VM to run and answer on ssh. Change "prov-db-host-reboot-timeout" to: '
                onSave={(value) => dispatch(setSetting({ clusterName: selectedCluster?.name, setting: 'prov-db-host-reboot-timeout', value: value }))}
              />
            )
          }
        ]
      : []),
    {
      clusterSetting: 'prov-docker-daemon-private',
      key: 'Provisioning Private Docker Daemon',
      value: (
        <RMSwitch
          isChecked={selectedCluster?.config?.provDockerDaemonPrivate}
          isDisabled={user?.grants['cluster-settings'] == false}
          confirmTitle={'Confirm switch settings for prov-docker-daemon-private?'}
          onChange={() =>
            dispatch(switchSetting({ clusterName: selectedCluster?.name, setting: 'prov-docker-daemon-private' }))
          }
        />
      )
    },
    {
      clusterSetting: 'prov-object-allow-overwrite',
      key: 'Provisioning Allow Overwrite Config/Secret Objects',
      value: (
        <RMSwitch
          isChecked={selectedCluster?.config?.provObjectAllowOverwrite}
          isDisabled={user?.grants['cluster-settings'] == false}
          confirmTitle={'Confirm switch settings for prov-object-allow-overwrite?'}
          onChange={() =>
            dispatch(switchSetting({ clusterName: selectedCluster?.name, setting: 'prov-object-allow-overwrite' }))
          }
        />
      )
    }
  ].filter(Boolean)
  return (
    <VStack>
      <TableType2 dataArray={scopedRows(dataObject, scope)} className={parentStyles.table} />
    </VStack>
  )
}

export default OrchestratorDbVM
