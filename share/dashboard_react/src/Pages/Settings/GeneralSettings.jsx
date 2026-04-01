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

  const h = (content, title) => (
    <RMIconButton icon={HiQuestionMarkCircle} onClick={() => openInfoModal(title, content)} iconFontsize='1rem' variant='ghost' style={{ opacity: 0.5, minWidth: '1.5rem', height: '1.5rem' }} />
  )

  const { settings: { failoverLoading, targetTopologyLoading, allowUnsafeClusterLoading, allowMultiMasterConcurrentWriteLoading, allowMultitierSlaveLoading, testLoading } } = useSelector((state) => state)

  useEffect(() => {
    if (selectedCluster?.topologyType) setTopologyOptions(convertObjectToArrayForDropdown(selectedCluster.topologyType))
  }, [selectedCluster?.topologyType])

  const hFailoverMode = `**Failover Mode**\n\nControls whether failover is triggered automatically or requires manual intervention.\n\n- **On-call (manual)** — waits for an operator to confirm before promoting a replica.\n- **On-leave (auto)** — promotes the best replica automatically when failover conditions are met.\n\nConfig: \`failover-mode\``
  const hTopology = `**Target Topology**\n\nSets the preferred replication topology for this cluster.\nUsed as the reference when enforcing replication configuration or after a failover/switchover.\n\nConfig: \`topology-target\``
  const hConcurrentWrite = `**Allow Concurrent Write on Multi-Master**\n\nWhen enabled, write traffic is accepted on all masters simultaneously.\nDisable to route writes to a single master and treat others as warm standbys.\n\nConfig: \`replication-multi-master-concurrent-write\``
  const hRingUnsafe = `**Allow Multi-Master Ring on Unsafe Cluster**\n\nPermits ring topology even when the cluster is in an unsafe state (lagging or unreachable node).\nOnly enable if you understand the risk of split-brain in a ring.\n\nConfig: \`replication-multi-master-ring-unsafe\``
  const hMultiTier = `**Allow Multi-Tier Slave**\n\nWhen enabled, replicas can have downstream replicas (chained replication).\nWhen disabled, replication-manager enforces a flat star topology.\n\nConfig: \`replication-no-relay\``
  const hTestMode = `**Test Mode**\n\nEnables regression test injection and simulated failure scenarios.\nDo not enable on production clusters.\n\nConfig: \`test\``
  const hClusterName = `**Cluster Name**\n\nAlpha-numeric identifier for this cluster (letters, digits, \`-\`, \`_\` only).\nRenaming updates all configuration references.\n\nConfig: \`cluster-name\``
  const hDropCluster = `**Drop Cluster**\n\nPermanently removes this cluster from replication-manager.\nAll configuration, state files and scheduled tasks are deleted.\n**This action cannot be undone.**\n\nConfig: \`drop-cluster (action)\``

  const dataObject = [
    { key: 'Target Topology', help: h(hTopology, 'Target Topology'), value: (<Flex className={styles.dropdownContainer}><Dropdown options={topologyOptions} className={styles.dropdownButton} selectedValue={selectedCluster?.config?.topologyTarget} confirmTitle={`Please confirm if you want to set the preferred topology to`} onChange={(t) => dispatch(changeTopology({ clusterName: selectedCluster?.name, topology: t }))} />{targetTopologyLoading && <Spinner />}</Flex>) },
    { key: 'Concurrent Write on Multi-Master', help: h(hConcurrentWrite, 'Concurrent Write on Multi-Master'), value: (<RMSwitch isChecked={selectedCluster?.config?.replicationMultiMasterConcurrentWrite} isDisabled={user?.grants['cluster-settings'] == false} loading={allowMultiMasterConcurrentWriteLoading} confirmTitle={'Confirm switch settings for multi-master-concurrent-write?'} onChange={() => dispatch(switchSetting({ clusterName: selectedCluster?.name, setting: 'multi-master-concurrent-write' }))} />) },
    { key: 'Multi-Master Ring When Unsafe', help: h(hRingUnsafe, 'Multi-Master Ring When Unsafe'), value: (<RMSwitch isChecked={selectedCluster?.config?.replicationMultiMasterRingUnsafe} isDisabled={user?.grants['cluster-settings'] == false} loading={allowUnsafeClusterLoading} confirmTitle={'Confirm switch settings for multi-master-ring-unsafe?'} onChange={() => dispatch(switchSetting({ clusterName: selectedCluster?.name, setting: 'multi-master-ring-unsafe' }))} />) },
    { key: 'Allow Multi-Tier Slave', help: h(hMultiTier, 'Allow Multi-Tier Slave'), value: (<RMSwitch isChecked={!selectedCluster?.config?.replicationMasterSlaveNeverRelay} isDisabled={user?.grants['cluster-settings'] == false} loading={allowMultitierSlaveLoading} confirmTitle={'Confirm switch settings for replication-no-relay?'} onChange={() => dispatch(switchSetting({ clusterName: selectedCluster?.name, setting: 'replication-no-relay' }))} />) },
    { key: 'Test Mode', help: h(hTestMode, 'Test Mode'), value: (<RMSwitch isChecked={selectedCluster?.config?.test} isDisabled={user?.grants['cluster-settings'] == false} loading={testLoading} confirmTitle={'Confirm switch settings for test?'} onChange={() => dispatch(switchSetting({ clusterName: selectedCluster?.name, setting: 'test' }))} />) },
    { key: 'Cluster Name (alpha-numeric)', help: h(hClusterName, 'Cluster Name'), value: (<TextForm value={selectedCluster?.name} confirmTitle={`Confirm rename cluster to `} regexPattern={'^[a-zA-Z0-9_-]*$'} onSave={(value) => { dispatch(renameCluster({ clusterName: selectedCluster?.name, newClusterName: value })).then(() => { onTabChange(0) }) }} />) },
    { key: 'Drop Cluster', help: h(hDropCluster, 'Drop Cluster'), value: (<RMIconButton icon={TbTrash} onClick={() => { openConfirmModal(`Confirm drop cluster? This action can not be undone!`, () => () => { dispatch(dropCluster({ clusterName: selectedCluster?.name })).then(() => { onTabChange(0) }) }) }} />) },
  ]

  return (
    <>
      <Flex justify='space-between' gap='0'>
        <TableType2 dataArray={dataObject} className={styles.tableWithHelp} helpColumn={true} />
      </Flex>
      <CommonModal isOpen={isCommonModalOpen} closeModal={() => setIsCommonModalOpen(false)} title={action.title} body={action.body} size='xl' />
    </>
  )
}

export default GeneralSettings
