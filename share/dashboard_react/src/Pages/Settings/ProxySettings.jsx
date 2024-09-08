import { Flex } from '@chakra-ui/react'
import React from 'react'
import styles from './styles.module.scss'
import RMSwitch from '../../components/RMSwitch'

import { useDispatch } from 'react-redux'
import TableType2 from '../../components/TableType2'
import { setSetting, switchSetting } from '../../redux/settingsSlice'
import RMSlider from '../../components/Sliders/RMSlider'

function ProxySettings({ selectedCluster, user, openConfirmModal }) {
  const dispatch = useDispatch()

  const dataObject = [
    {
      key: 'ProxySQL Monitor',
      value: (
        <RMSwitch
          confirmTitle={'Confirm switch settings for proxysql?'}
          onChange={() => dispatch(switchSetting({ clusterName: selectedCluster?.name, setting: 'proxysql' }))}
          isDisabled={user?.grants['cluster-settings'] == false}
          isChecked={selectedCluster?.config?.proxysql}
        />
      )
    },
    {
      key: 'ProxySQL Bootstrap Servers',
      value: (
        <RMSwitch
          confirmTitle={'Confirm switch settings for proxysql-bootstrap-servers?'}
          onChange={() =>
            dispatch(switchSetting({ clusterName: selectedCluster?.name, setting: 'proxysql-bootstrap-servers' }))
          }
          isDisabled={user?.grants['cluster-settings'] == false}
          isChecked={selectedCluster?.config?.proxysqlBootstrap}
        />
      )
    },
    {
      key: 'ProxySQL Bootsrap Users',
      value: (
        <RMSwitch
          confirmTitle={'Confirm switch settings for proxysql-bootstrap-users?'}
          onChange={() =>
            dispatch(switchSetting({ clusterName: selectedCluster?.name, setting: 'proxysql-bootstrap-users' }))
          }
          isDisabled={user?.grants['cluster-settings'] == false}
          isChecked={selectedCluster?.config?.proxysqlBootstrapyUsers}
        />
      )
    },
    {
      key: 'ProxySQL Bootsrap Variables',
      value: (
        <RMSwitch
          confirmTitle={'Confirm switch settings for proxysql-bootstrap-variables?'}
          onChange={() =>
            dispatch(switchSetting({ clusterName: selectedCluster?.name, setting: 'proxysql-bootstrap-variables' }))
          }
          isDisabled={user?.grants['cluster-settings'] == false}
          isChecked={selectedCluster?.config?.proxysqlBootstrapVariables}
        />
      )
    },
    {
      key: 'ProxySQL Bootsrap Hostgroups',
      value: (
        <RMSwitch
          confirmTitle={'Confirm switch settings for proxysql-bootstrap-hostgroups?'}
          onChange={() =>
            dispatch(switchSetting({ clusterName: selectedCluster?.name, setting: 'proxysql-bootstrap-hostgroups' }))
          }
          isDisabled={user?.grants['cluster-settings'] == false}
          isChecked={selectedCluster?.config?.proxysqlBootstrapHostgroups}
        />
      )
    },
    {
      key: 'ProxySQL Bootsrap Query Rules`',
      value: (
        <RMSwitch
          confirmTitle={'Confirm switch settings for proxysql-bootstrap-query-rules?'}
          onChange={() =>
            dispatch(switchSetting({ clusterName: selectedCluster?.name, setting: 'proxysql-bootstrap-query-rules' }))
          }
          isDisabled={user?.grants['cluster-settings'] == false}
          isChecked={selectedCluster?.config?.proxysqlBootstrapQueryRules}
        />
      )
    },
    {
      key: 'ProxySQL Bootsrap Query Rules`',
      value: (
        <RMSwitch
          confirmTitle={'Confirm switch settings for proxysql-bootstrap-query-rules?'}
          onChange={() =>
            dispatch(switchSetting({ clusterName: selectedCluster?.name, setting: 'proxysql-bootstrap-query-rules' }))
          }
          isDisabled={user?.grants['cluster-settings'] == false}
          isChecked={selectedCluster?.config?.proxysqlBootstrapQueryRules}
        />
      )
    },
    {
      key: 'Proxies compression to backends',
      value: (
        <RMSwitch
          confirmTitle={'Confirm switch settings for proxy-servers-backend-compression?'}
          onChange={() =>
            dispatch(
              switchSetting({ clusterName: selectedCluster?.name, setting: 'proxy-servers-backend-compression' })
            )
          }
          isDisabled={user?.grants['cluster-settings'] == false}
          isChecked={selectedCluster?.config?.proxyServersBackendCompression}
        />
      )
    },
    {
      key: 'Proxies reads on writer',
      value: (
        <RMSwitch
          confirmTitle={'Confirm switch settings for proxy-servers-read-on-master?'}
          onChange={() =>
            dispatch(switchSetting({ clusterName: selectedCluster?.name, setting: 'proxy-servers-read-on-master' }))
          }
          isDisabled={user?.grants['cluster-settings'] == false}
          isChecked={selectedCluster?.config?.proxyServersReadOnMaster}
        />
      )
    },
    {
      key: 'Proxies reads on writer when no slave',
      value: (
        <RMSwitch
          confirmTitle={'Confirm switch settings for proxy-servers-read-on-master-no-slave?'}
          onChange={() =>
            dispatch(
              switchSetting({ clusterName: selectedCluster?.name, setting: 'proxy-servers-read-on-master-no-slave' })
            )
          }
          isDisabled={user?.grants['cluster-settings'] == false}
          isChecked={selectedCluster?.config?.proxyServersReadOnMasterNoSlave}
        />
      )
    },
    {
      key: 'Proxies Max Backend Connections',
      value: (
        <RMSlider
          value={selectedCluster?.config?.proxyServersBackendMaxConnections}
          min={100}
          max={10000}
          step={100}
          showMarkAtInterval={2000}
          selectedMarkLabelCSS={styles.maxConnectMarkLabel}
          confirmTitle='Confirm change backends max connections : '
          onChange={(val) =>
            dispatch(
              setSetting({
                clusterName: selectedCluster?.name,
                setting: 'proxy-servers-backend-max-connections',
                value: val
              })
            )
          }
        />
      )
    },
    {
      key: 'Proxies Max Backend Repl Lag for Reads',
      value: (
        <RMSlider
          value={selectedCluster?.config?.proxyServersBackendMaxReplicationLag}
          min={10}
          max={5000}
          step={1}
          showMarkAtInterval={1000}
          selectedMarkLabelCSS={styles.maxConnectMarkLabel}
          confirmTitle='Confirm change delay : '
          onChange={(val) =>
            dispatch(
              setSetting({
                clusterName: selectedCluster?.name,
                setting: 'proxy-servers-backend-max-replication-lag',
                value: val
              })
            )
          }
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

export default ProxySettings
