import { Box, Flex, Spinner } from '@chakra-ui/react'
import React, { useState, useEffect } from 'react'
import styles from './styles.module.scss'
import RMSwitch from '../../components/RMSwitch'
import Dropdown from '../../components/Dropdown'
import { convertObjectToArrayForDropdown } from '../../utility/common'
import { useDispatch, useSelector } from 'react-redux'
import TableType2 from '../../components/TableType2'
import { changeTopology, switchSetting } from '../../redux/settingsSlice'
import { dropCluster, renameCluster } from '../../redux/globalClustersSlice'
import { TbTrash } from 'react-icons/tb'
import RMIconButton from '../../components/RMIconButton'
import TextForm from '../../components/TextForm'
import CommonModal from '../../components/Modals/CommonModal'
import modalStyles from '../../components/Modals/styles.module.scss'
import Markdown from 'react-markdown'
import remarkGfm from 'remark-gfm'
import { HiQuestionMarkCircle } from 'react-icons/hi'

function GeneralSettings({ selectedCluster, user, openConfirmModal, onTabChange }) {
  const [topologyOptions, setTopologyOptions] = useState([])
  const dispatch = useDispatch()
  const [action, setAction] = useState({ title: '', body: <></> })
  const [isCommonModalOpen, setIsCommonModalOpen] = useState(false)

  const openInfoModal = (title, content) => {
    setAction({ title, body: <Box className={modalStyles.infoTooltip}><Markdown remarkPlugins={[remarkGfm]}>{content}</Markdown></Box> })
    setIsCommonModalOpen(true)
  }

  const helpKey = (label, content) => (
    <Box as="span" display="inline">
      {label}
      <Box as="span" display="inline-flex" verticalAlign="middle" ml={1}>
        <RMIconButton icon={HiQuestionMarkCircle} onClick={() => openInfoModal(label, content)} />
      </Box>
    </Box>
  )

  const {
    settings: { failoverLoading, targetTopologyLoading, allowUnsafeClusterLoading, allowMultiMasterConcurrentWriteLoading, allowMultitierSlaveLoading, testLoading }
  } = useSelector((state) => state)

  useEffect(() => {
    if (selectedCluster?.topologyType) {
      setTopologyOptions(convertObjectToArrayForDropdown(selectedCluster.topologyType))
    }
  }, [selectedCluster?.topologyType])

  const helpFailoverMode = `**Failover Mode**\n\nControls whether failover is triggered automatically or requires manual intervention.\n\n- **On-call (manual)** — replication-manager detects the failure and alerts, but waits for an operator to confirm before promoting a replica.\n- **On-leave (auto)** — replication-manager promotes the best replica automatically when failover conditions are met.\n\nManual mode is safer for environments where false positives carry high risk.`
  const helpTopology = `**Target Topology**\n\nSets the preferred replication topology for this cluster.\nreplication-manager uses this as the reference when enforcing replication configuration or after a failover/switchover.\n\nAvailable values depend on the cluster's configured topology type.`
  const helpConcurrentWrite = `**Allow Concurrent Write on Multi-Master**\n\nWhen enabled, write traffic is accepted on all masters in a multi-master topology simultaneously.\nDisable to route writes to a single master and treat others as warm standbys.`
  const helpRingUnsafe = `**Allow Multi-Master Ring on Unsafe Cluster**\n\nPermits ring topology even when the cluster is in an unsafe state (e.g. a node is lagging or unreachable).\nOnly enable this if you understand the risk of split-brain in a ring.`
  const helpMultiTier = `**Allow Multi-Tier Slave**\n\nWhen enabled, replicas can themselves have downstream replicas (chained replication / relay slaves).\nWhen disabled (\`replication-no-relay\`), replication-manager enforces a flat star topology where all replicas connect directly to the master.`
  const helpTestMode = `**Test Mode**\n\nEnables regression test injection and simulated failure scenarios.\nDo not enable on production clusters — test mode allows artificial delay and error injection that can destabilise replication.`
  const helpClusterName = `**Cluster Name**\n\nAlpha-numeric identifier for this cluster (letters, digits, \`-\`, \`_\` only).\nRenaming a cluster updates all configuration references and requires a page reload.`
  const helpDropCluster = `**Drop Cluster**\n\nPermanently removes this cluster from replication-manager.\nAll configuration, state files and scheduled tasks for this cluster are deleted.\n**This action cannot be undone.**`

  const dataObject = [
    {
      key: helpKey('Failover Mode (interactive)', helpFailoverMode),
      value: (<RMSwitch onText='On-call (manual)' offText='On-leave (auto)' confirmTitle={'Confirm switch settings for failover-mode?'} onChange={() => dispatch(switchSetting({ clusterName: selectedCluster?.name, setting: 'failover-mode' }))} isDisabled={user?.grants['cluster-settings'] == false} isChecked={selectedCluster?.config?.interactive} loading={failoverLoading} />)
    },
    {
      key: helpKey('Target Topology', helpTopology),
      value: (
        <Flex className={styles.dropdownContainer}>
          <Dropdown options={topologyOptions} className={styles.dropdownButton} selectedValue={selectedCluster?.config?.topologyTarget} confirmTitle={`Please confirm if you want to set the preferred topology to`} onChange={(selectedTopology) => dispatch(changeTopology({ clusterName: selectedCluster?.name, topology: selectedTopology }))} />
          {targetTopologyLoading && <Spinner />}
        </Flex>
      )
    },
    { key: helpKey('Allow Concurrent Write on Multi-Master Topology', helpConcurrentWrite), value: (<RMSwitch isChecked={selectedCluster?.config?.replicationMultiMasterConcurrentWrite} isDisabled={user?.grants['cluster-settings'] == false} loading={allowMultiMasterConcurrentWriteLoading} confirmTitle={'Confirm switch settings for multi-master-concurrent-write?'} onChange={() => dispatch(switchSetting({ clusterName: selectedCluster?.name, setting: 'multi-master-concurrent-write' }))} />) },
    { key: helpKey('Allow Multi-Master Ring Topology on Unsafe Cluster', helpRingUnsafe), value: (<RMSwitch isChecked={selectedCluster?.config?.replicationMultiMasterRingUnsafe} isDisabled={user?.grants['cluster-settings'] == false} loading={allowUnsafeClusterLoading} confirmTitle={'Confirm switch settings for multi-master-ring-unsafe?'} onChange={() => dispatch(switchSetting({ clusterName: selectedCluster?.name, setting: 'multi-master-ring-unsafe' }))} />) },
    { key: helpKey('Allow Multi-Tier Slave', helpMultiTier), value: (<RMSwitch isChecked={!selectedCluster?.config?.replicationMasterSlaveNeverRelay} isDisabled={user?.grants['cluster-settings'] == false} loading={allowMultitierSlaveLoading} confirmTitle={'Confirm switch settings for replication-no-relay?'} onChange={() => dispatch(switchSetting({ clusterName: selectedCluster?.name, setting: 'replication-no-relay' }))} />) },
    { key: helpKey('Test Mode', helpTestMode), value: (<RMSwitch isChecked={selectedCluster?.config?.test} isDisabled={user?.grants['cluster-settings'] == false} loading={testLoading} confirmTitle={'Confirm switch settings for test?'} onChange={() => dispatch(switchSetting({ clusterName: selectedCluster?.name, setting: 'test' }))} />) },
    { key: helpKey('Cluster Name (alpha-numeric)', helpClusterName), value: (<TextForm value={selectedCluster?.name} confirmTitle={`Confirm rename cluster to `} regexPattern={'^[a-zA-Z0-9_-]*$'} onSave={(value) => { dispatch(renameCluster({ clusterName: selectedCluster?.name, newClusterName: value })).then(() => { onTabChange(0) }) }} />) },
    {
      key: helpKey('Drop Cluster', helpDropCluster),
      value: (<RMIconButton icon={TbTrash} onClick={() => { openConfirmModal(`Confirm drop cluster? This action can not be undone!`, () => () => { dispatch(dropCluster({ clusterName: selectedCluster?.name })).then(() => { onTabChange(0) }) }) }} />)
    }
  ]

  return (
    <>
      <Flex justify='space-between' gap='0'>
        <TableType2 dataArray={dataObject} className={styles.table} />
      </Flex>
      <CommonModal isOpen={isCommonModalOpen} closeModal={() => setIsCommonModalOpen(false)} title={action.title} body={action.body} size='xl' />
    </>
  )
}

export default GeneralSettings
