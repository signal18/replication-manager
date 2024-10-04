import { Flex } from '@chakra-ui/react'
import React from 'react'
import styles from './styles.module.scss'
import RMSwitch from '../../components/RMSwitch'
import { useDispatch } from 'react-redux'
import TableType2 from '../../components/TableType2'
import { switchGlobalSetting } from '../../redux/globalClustersSlice'
import TextForm from '../../components/TextForm'

function CloudSettings({ monitor }) {
  const dispatch = useDispatch()

  const dataObject = [
    {
      key: 'Cloud18',
      value: (
        <RMSwitch
          confirmTitle={'Confirm switch global settings for cloud18?'}
          onChange={() => dispatch(switchGlobalSetting({ setting: 'cloud18' }))}
          isChecked={monitor?.config?.cloud18}
        />
      )
    },
    {
      key: 'Domain',
      value: (
        <TextForm
          value={monitor?.config?.cloud18Domain}
          confirmTitle={`Confirm cloud18 Domain to `}
          onSave={(value) => {
            // dispatch(
            //   setSetting({
            //     clusterName: selectedCluster?.name,
            //     setting: 'prov-db-volume-data',
            //     value: value
            //   })
            // )
          }}
        />
      )
    },
    {
      key: 'Git user',
      value: (
        <TextForm
          value={monitor?.config?.cloud18GitUser}
          confirmTitle={`Confirm git username to `}
          onSave={(value) => {
            // dispatch(
            //   setSetting({
            //     clusterName: selectedCluster?.name,
            //     setting: 'prov-db-volume-data',
            //     value: value
            //   })
            // )
          }}
        />
      )
    },
    {
      key: 'Gitlab Password',
      value: (
        <TextForm
          value={monitor?.config?.cloud18GitlabPassword}
          confirmTitle={`Confirm gitlab password to `}
          onSave={(value) => {
            // dispatch(
            //   setSetting({
            //     clusterName: selectedCluster?.name,
            //     setting: 'prov-db-volume-data',
            //     value: value
            //   })
            // )
          }}
        />
      )
    },
    {
      key: 'Platform Description',
      value: (
        <TextForm
          value={monitor?.config?.cloud18PlatformDescription}
          confirmTitle={`Confirm platform description to `}
          onSave={(value) => {
            // dispatch(
            //   setSetting({
            //     clusterName: selectedCluster?.name,
            //     setting: 'prov-db-volume-data',
            //     value: value
            //   })
            // )
          }}
        />
      )
    },
    {
      key: 'Shared',
      value: (
        <RMSwitch
          confirmTitle={'Confirm switch global settings for shared cloud18?'}
          onChange={() => dispatch(switchGlobalSetting({ setting: 'cloud18Shared' }))}
          isChecked={monitor?.config?.cloud18Shared}
        />
      )
    },
    {
      key: 'Subdomain',
      value: (
        <TextForm
          value={monitor?.config?.cloud18SubDomain}
          confirmTitle={`Confirm subdomain to `}
          onSave={(value) => {
            // dispatch(
            //   setSetting({
            //     clusterName: selectedCluster?.name,
            //     setting: 'prov-db-volume-data',
            //     value: value
            //   })
            // )
          }}
        />
      )
    },
    {
      key: 'Subdomain zone',
      value: (
        <TextForm
          value={monitor?.config?.cloud18SubDomainZone}
          confirmTitle={`Confirm subdomain zone to `}
          onSave={(value) => {
            // dispatch(
            //   setSetting({
            //     clusterName: selectedCluster?.name,
            //     setting: 'prov-db-volume-data',
            //     value: value
            //   })
            // )
          }}
        />
      )
    }
  ]

  return (
    <Flex justify='space-between' gap='0'>
      <TableType2 dataArray={dataObject} className={styles.table} />
    </Flex>
  )
}

export default CloudSettings
