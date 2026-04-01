import { Box, Flex } from '@chakra-ui/react'
import React, { useState } from 'react'
import styles from './styles.module.scss'
import RMSwitch from '../../components/RMSwitch'
import { useDispatch } from 'react-redux'
import TableType2 from '../../components/TableType2'
import { setSetting, switchSetting } from '../../redux/settingsSlice'
import RMSlider from '../../components/Sliders/RMSlider'
import CommonModal from '../../components/Modals/CommonModal'
import modalStyles from '../../components/Modals/styles.module.scss'
import Markdown from 'react-markdown'
import remarkGfm from 'remark-gfm'
import { HiQuestionMarkCircle } from 'react-icons/hi'
import RMIconButton from '../../components/RMIconButton'

function ProxySettings({ selectedCluster, user, openConfirmModal }) {
  const dispatch = useDispatch()
  const [action, setAction] = useState({ title: '', body: <></> })
  const [isCommonModalOpen, setIsCommonModalOpen] = useState(false)

  const openInfoModal = (title, content) => {
    setAction({ title, body: <Box className={modalStyles.infoTooltip}><Markdown remarkPlugins={[remarkGfm]}>{content}</Markdown></Box> })
    setIsCommonModalOpen(true)
  }

  const h = (content, title) => <RMIconButton icon={HiQuestionMarkCircle} onClick={() => openInfoModal(title, content)} />
  const sw = (setting, configKey) => <RMSwitch confirmTitle={`Confirm switch settings for ${setting}?`} onChange={() => dispatch(switchSetting({ clusterName: selectedCluster?.name, setting }))} isDisabled={user?.grants['cluster-settings'] == false} isChecked={selectedCluster?.config?.[configKey]} />

  const hMonitor = `**ProxySQL Monitor**\n\nEnables replication-manager's ProxySQL integration.\nKeeps ProxySQL's server list, hostgroups, and routing rules in sync with the current cluster topology.\n\nConfig: \`proxysql\``
  const hServers = `**ProxySQL Bootstrap Servers**\n\nPushes the current cluster server list into ProxySQL's \`mysql_servers\` table.\n\nConfig: \`proxysql-bootstrap-servers\``
  const hUsers = `**ProxySQL Bootstrap Users**\n\nCopies MySQL user grants into ProxySQL's \`mysql_users\` table.\n\nConfig: \`proxysql-bootstrap-users\``
  const hVars = `**ProxySQL Bootstrap Variables**\n\nApplies recommended ProxySQL global variables from the replication-manager configuration.\n\nConfig: \`proxysql-bootstrap-variables\``
  const hHG = `**ProxySQL Bootstrap Hostgroups**\n\nConfigures ProxySQL hostgroups (writer, reader, backup-writer) based on the current cluster topology.\n\nConfig: \`proxysql-bootstrap-hostgroups\``
  const hQR = `**ProxySQL Bootstrap Query Rules**\n\nLoads default query routing rules into ProxySQL. Existing rules are replaced.\n\nConfig: \`proxysql-bootstrap-query-rules\``
  const hCompress = `**Proxies Compression to Backends**\n\nEnables MySQL protocol compression on connections between ProxySQL and the backend servers.\nReduces network bandwidth at the cost of additional CPU.\n\nConfig: \`proxy-servers-backend-compression\``
  const hReadsWriter = `**Proxies Reads on Writer**\n\nRoutes read queries to the writer (master) node as well.\nUseful when strong read consistency is required or replica lag is too high.\n\nConfig: \`proxy-servers-read-on-master\``
  const hReadsNoSlave = `**Proxies Reads on Writer When No Slave**\n\nFallback: automatically routes reads to the writer when no healthy replica is available.\n\nConfig: \`proxy-servers-read-on-master-no-slave\``
  const hMaxConn = `**Proxies Max Backend Connections**\n\nMaximum connections ProxySQL will open to each backend server.\nTune according to the \`max_connections\` setting on your MySQL servers.\n\nConfig: \`proxy-servers-backend-max-connections\``
  const hMaxLag = `**Proxies Max Backend Replication Lag for Reads**\n\nMaximum replication lag (in seconds) a replica may have before ProxySQL stops routing reads to it.\n\nConfig: \`proxy-servers-backend-max-replication-lag\``

  const dataObject = [
    { key: 'ProxySQL Monitor', help: h(hMonitor, 'ProxySQL Monitor'), value: sw('proxysql', 'proxysql') },
    { key: 'ProxySQL Bootstrap Servers', help: h(hServers, 'ProxySQL Bootstrap Servers'), value: sw('proxysql-bootstrap-servers', 'proxysqlBootstrap') },
    { key: 'ProxySQL Bootstrap Users', help: h(hUsers, 'ProxySQL Bootstrap Users'), value: sw('proxysql-bootstrap-users', 'proxysqlBootstrapUsers') },
    { key: 'ProxySQL Bootstrap Variables', help: h(hVars, 'ProxySQL Bootstrap Variables'), value: sw('proxysql-bootstrap-variables', 'proxysqlBootstrapVariables') },
    { key: 'ProxySQL Bootstrap Hostgroups', help: h(hHG, 'ProxySQL Bootstrap Hostgroups'), value: sw('proxysql-bootstrap-hostgroups', 'proxysqlBootstrapHostgroups') },
    { key: 'ProxySQL Bootstrap Query Rules', help: h(hQR, 'ProxySQL Bootstrap Query Rules'), value: sw('proxysql-bootstrap-query-rules', 'proxysqlBootstrapQueryRules') },
    { key: 'Proxies Compression to Backends', help: h(hCompress, 'Proxies Compression to Backends'), value: sw('proxy-servers-backend-compression', 'proxyServersBackendCompression') },
    { key: 'Proxies Reads on Writer', help: h(hReadsWriter, 'Proxies Reads on Writer'), value: sw('proxy-servers-read-on-master', 'proxyServersReadOnMaster') },
    { key: 'Proxies Reads on Writer When No Slave', help: h(hReadsNoSlave, 'Proxies Reads on Writer When No Slave'), value: sw('proxy-servers-read-on-master-no-slave', 'proxyServersReadOnMasterNoSlave') },
    { key: 'Proxies Max Backend Connections', help: h(hMaxConn, 'Proxies Max Backend Connections'), value: (<RMSlider value={selectedCluster?.config?.proxyServersBackendMaxConnections} min={100} max={10000} step={100} showMarkAtInterval={2000} selectedMarkLabelCSS={styles.maxConnectMarkLabel} confirmTitle='Confirm change backends max connections : ' onChange={(val) => dispatch(setSetting({ clusterName: selectedCluster?.name, setting: 'proxy-servers-backend-max-connections', value: val }))} />) },
    { key: 'Proxies Max Backend Replication Lag for Reads', help: h(hMaxLag, 'Proxies Max Backend Replication Lag for Reads'), value: (<RMSlider value={selectedCluster?.config?.proxyServersBackendMaxReplicationLag} min={10} max={5000} step={1} showMarkAtInterval={1000} selectedMarkLabelCSS={styles.maxConnectMarkLabel} confirmTitle='Confirm change delay : ' onChange={(val) => dispatch(setSetting({ clusterName: selectedCluster?.name, setting: 'proxy-servers-backend-max-replication-lag', value: val }))} />) },
  ]

  return (
    <>
      <Flex justify='space-between' gap='0'>
        <TableType2 dataArray={dataObject} className={styles.table} helpColumn={true} />
      </Flex>
      <CommonModal isOpen={isCommonModalOpen} closeModal={() => setIsCommonModalOpen(false)} title={action.title} body={action.body} size='xl' />
    </>
  )
}

export default ProxySettings
