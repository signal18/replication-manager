import { Box, Flex, HStack } from '@chakra-ui/react'
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

  const helpKey = (label, content) => (
    <HStack spacing={1} align="center">
      <span>{label}</span>
      <RMIconButton icon={HiQuestionMarkCircle} onClick={() => openInfoModal(label, content)} />
    </HStack>
  )

  const helpProxySQLMonitor = `**ProxySQL Monitor**\n\nEnables replication-manager's ProxySQL integration.\nWhen active, replication-manager connects to ProxySQL and keeps its server list, hostgroups, and routing rules in sync with the current cluster topology.`
  const helpBootstrapServers = `**ProxySQL Bootstrap Servers**\n\nPushes the current cluster server list into ProxySQL's \`mysql_servers\` table.\nRun this once after initial setup or after adding/removing nodes to synchronise ProxySQL with the cluster.`
  const helpBootstrapUsers = `**ProxySQL Bootstrap Users**\n\nCopies MySQL user grants into ProxySQL's \`mysql_users\` table.\nEnsures application users defined in MySQL are available in ProxySQL without manual configuration.`
  const helpBootstrapVariables = `**ProxySQL Bootstrap Variables**\n\nApplies recommended ProxySQL global variables from the replication-manager configuration.\nCovers connection pool sizes, timeouts, and monitoring credentials.`
  const helpBootstrapHostgroups = `**ProxySQL Bootstrap Hostgroups**\n\nConfigures ProxySQL hostgroups (writer, reader, backup-writer) based on the current cluster topology.\nRe-run after topology changes to realign read/write routing.`
  const helpBootstrapQueryRules = `**ProxySQL Bootstrap Query Rules**\n\nLoads default query routing rules into ProxySQL.\nRules typically route writes to the writer hostgroup and reads to the reader hostgroup.\nExisting rules are replaced.`
  const helpCompression = `**Proxies Compression to Backends**\n\nEnables MySQL protocol compression on connections between ProxySQL and the backend database servers.\nReduces network bandwidth at the cost of additional CPU on both sides.\nMost beneficial over high-latency or low-bandwidth links.`
  const helpReadsOnWriter = `**Proxies Reads on Writer**\n\nWhen enabled, read queries are also routed to the writer (master) node.\nUseful when replica lag is too high to serve reads from replicas, or when strong read consistency is required.`
  const helpReadsOnWriterNoSlave = `**Proxies Reads on Writer When No Slave**\n\nFallback: automatically routes reads to the writer when no healthy replica is available.\nPrevents read failures during replica outages without requiring manual reconfiguration.`
  const helpMaxConn = `**Proxies Max Backend Connections**\n\nMaximum number of connections ProxySQL will open to each backend server.\nTune according to the \`max_connections\` setting on your MySQL servers.\nDefault: 1000.`
  const helpMaxLag = `**Proxies Max Backend Replication Lag for Reads**\n\nMaximum replication lag (in seconds) a replica may have before ProxySQL stops routing reads to it.\nReplicas exceeding this threshold are temporarily removed from the reader hostgroup.\nDefault: 10 seconds.`

  const dataObject = [
    { key: helpKey('ProxySQL Monitor', helpProxySQLMonitor), value: (<RMSwitch confirmTitle={'Confirm switch settings for proxysql?'} onChange={() => dispatch(switchSetting({ clusterName: selectedCluster?.name, setting: 'proxysql' }))} isDisabled={user?.grants['cluster-settings'] == false} isChecked={selectedCluster?.config?.proxysql} />) },
    { key: helpKey('ProxySQL Bootstrap Servers', helpBootstrapServers), value: (<RMSwitch confirmTitle={'Confirm switch settings for proxysql-bootstrap-servers?'} onChange={() => dispatch(switchSetting({ clusterName: selectedCluster?.name, setting: 'proxysql-bootstrap-servers' }))} isDisabled={user?.grants['cluster-settings'] == false} isChecked={selectedCluster?.config?.proxysqlBootstrap} />) },
    { key: helpKey('ProxySQL Bootstrap Users', helpBootstrapUsers), value: (<RMSwitch confirmTitle={'Confirm switch settings for proxysql-bootstrap-users?'} onChange={() => dispatch(switchSetting({ clusterName: selectedCluster?.name, setting: 'proxysql-bootstrap-users' }))} isDisabled={user?.grants['cluster-settings'] == false} isChecked={selectedCluster?.config?.proxysqlBootstrapUsers} />) },
    { key: helpKey('ProxySQL Bootstrap Variables', helpBootstrapVariables), value: (<RMSwitch confirmTitle={'Confirm switch settings for proxysql-bootstrap-variables?'} onChange={() => dispatch(switchSetting({ clusterName: selectedCluster?.name, setting: 'proxysql-bootstrap-variables' }))} isDisabled={user?.grants['cluster-settings'] == false} isChecked={selectedCluster?.config?.proxysqlBootstrapVariables} />) },
    { key: helpKey('ProxySQL Bootstrap Hostgroups', helpBootstrapHostgroups), value: (<RMSwitch confirmTitle={'Confirm switch settings for proxysql-bootstrap-hostgroups?'} onChange={() => dispatch(switchSetting({ clusterName: selectedCluster?.name, setting: 'proxysql-bootstrap-hostgroups' }))} isDisabled={user?.grants['cluster-settings'] == false} isChecked={selectedCluster?.config?.proxysqlBootstrapHostgroups} />) },
    { key: helpKey('ProxySQL Bootstrap Query Rules', helpBootstrapQueryRules), value: (<RMSwitch confirmTitle={'Confirm switch settings for proxysql-bootstrap-query-rules?'} onChange={() => dispatch(switchSetting({ clusterName: selectedCluster?.name, setting: 'proxysql-bootstrap-query-rules' }))} isDisabled={user?.grants['cluster-settings'] == false} isChecked={selectedCluster?.config?.proxysqlBootstrapQueryRules} />) },
    { key: helpKey('Proxies Compression to Backends', helpCompression), value: (<RMSwitch confirmTitle={'Confirm switch settings for proxy-servers-backend-compression?'} onChange={() => dispatch(switchSetting({ clusterName: selectedCluster?.name, setting: 'proxy-servers-backend-compression' }))} isDisabled={user?.grants['cluster-settings'] == false} isChecked={selectedCluster?.config?.proxyServersBackendCompression} />) },
    { key: helpKey('Proxies Reads on Writer', helpReadsOnWriter), value: (<RMSwitch confirmTitle={'Confirm switch settings for proxy-servers-read-on-master?'} onChange={() => dispatch(switchSetting({ clusterName: selectedCluster?.name, setting: 'proxy-servers-read-on-master' }))} isDisabled={user?.grants['cluster-settings'] == false} isChecked={selectedCluster?.config?.proxyServersReadOnMaster} />) },
    { key: helpKey('Proxies Reads on Writer When No Slave', helpReadsOnWriterNoSlave), value: (<RMSwitch confirmTitle={'Confirm switch settings for proxy-servers-read-on-master-no-slave?'} onChange={() => dispatch(switchSetting({ clusterName: selectedCluster?.name, setting: 'proxy-servers-read-on-master-no-slave' }))} isDisabled={user?.grants['cluster-settings'] == false} isChecked={selectedCluster?.config?.proxyServersReadOnMasterNoSlave} />) },
    { key: helpKey('Proxies Max Backend Connections', helpMaxConn), value: (<RMSlider value={selectedCluster?.config?.proxyServersBackendMaxConnections} min={100} max={10000} step={100} showMarkAtInterval={2000} selectedMarkLabelCSS={styles.maxConnectMarkLabel} confirmTitle='Confirm change backends max connections : ' onChange={(val) => dispatch(setSetting({ clusterName: selectedCluster?.name, setting: 'proxy-servers-backend-max-connections', value: val }))} />) },
    { key: helpKey('Proxies Max Backend Replication Lag for Reads', helpMaxLag), value: (<RMSlider value={selectedCluster?.config?.proxyServersBackendMaxReplicationLag} min={10} max={5000} step={1} showMarkAtInterval={1000} selectedMarkLabelCSS={styles.maxConnectMarkLabel} confirmTitle='Confirm change delay : ' onChange={(val) => dispatch(setSetting({ clusterName: selectedCluster?.name, setting: 'proxy-servers-backend-max-replication-lag', value: val }))} />) },
  ]

  return (
    <>
      <Flex justify='space-between' gap='0'>
        <TableType2 dataArray={dataObject} className={styles.table} labelClassName={styles.labelWithHelp} />
      </Flex>
      <CommonModal isOpen={isCommonModalOpen} closeModal={() => setIsCommonModalOpen(false)} title={action.title} body={action.body} size='xl' />
    </>
  )
}

export default ProxySettings
