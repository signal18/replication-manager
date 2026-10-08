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

function OrchestratorDbVM({ selectedCluster, user }) {
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
    {
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
      <TableType2 dataArray={dataObject} className={parentStyles.table} />
    </VStack>
  )
}

export default OrchestratorDbVM
