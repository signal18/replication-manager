import { VStack } from '@chakra-ui/react'
import React, { useState, useEffect } from 'react'

import Dropdown from '../../../components/Dropdown'
import TableType2 from '../../../components/TableType2'
import parentStyles from '../styles.module.scss'
import { useDispatch, useSelector } from 'react-redux'
import { setSetting, switchSetting } from '../../../redux/settingsSlice'
import { convertObjectToArrayForDropdown } from '../../../utility/common'
import RMSwitch from '../../../components/RMSwitch'

function OrchestratorDbVM({ selectedCluster, user }) {
  const dispatch = useDispatch()
  const {
    cluster: { monitor }
  } = useSelector((state) => state)
  const [serviceVMs, setServiceVMs] = useState([])

  useEffect(() => {
    if (monitor?.serviceVM) {
      setServiceVMs(convertObjectToArrayForDropdown(monitor.serviceVM))
    }
  }, [monitor?.serviceVM])

  const dataObject = [
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
    }
  ]
  return (
    <VStack>
      <TableType2 dataArray={dataObject} className={parentStyles.table} />
    </VStack>
  )
}

export default OrchestratorDbVM
