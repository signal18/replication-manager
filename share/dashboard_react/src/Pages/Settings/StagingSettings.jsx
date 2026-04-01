import { Box, Flex } from '@chakra-ui/react'
import React, { useEffect, useState } from 'react'
import styles from './styles.module.scss'
import { useDispatch } from 'react-redux'
import TableType2 from '../../components/TableType2'
import { setSetting, switchSetting } from '../../redux/settingsSlice'
import TextForm from '../../components/TextForm'
import RMSwitch from '../../components/RMSwitch'
import RMIconButton from '../../components/RMIconButton'
import { TbDatabaseExport, TbDatabaseImport } from 'react-icons/tb'
import { refreshStaging, reseedStagingFromParent } from '../../redux/clusterSlice'
import Dropdown from '../../components/Dropdown'
import CommonModal from '../../components/Modals/CommonModal'
import modalStyles from '../../components/Modals/styles.module.scss'
import Markdown from 'react-markdown'
import remarkGfm from 'remark-gfm'
import { HiQuestionMarkCircle } from 'react-icons/hi'

function StagingSettings({ selectedCluster, user, openConfirmModal, monitor }) {
  const dispatch = useDispatch()
  const [clusters, setClusters] = useState([])
  const [action, setAction] = useState({ title: '', body: <></> })
  const [isCommonModalOpen, setIsCommonModalOpen] = useState(false)

  const openInfoModal = (title, content) => {
    setAction({ title, body: <Box className={modalStyles.infoTooltip}><Markdown remarkPlugins={[remarkGfm]}>{content}</Markdown></Box> })
    setIsCommonModalOpen(true)
  }

  const h = (content, title) => <RMIconButton icon={HiQuestionMarkCircle} onClick={() => openInfoModal(title, content)} iconFontsize='1rem' variant='ghost' style={{ opacity: 0.5, minWidth: '1.5rem', height: '1.5rem' }} />

  useEffect(() => {
    if (monitor?.clusters) setClusters(monitor?.clusters?.filter((cl) => cl != selectedCluster.name).map((cluster) => ({ name: cluster, value: cluster })))
  }, [monitor?.clusters?.join(',')])

  const hStaging = `**Topology Staging**\n\nEnables staging mode for this cluster.\nThe cluster acts as a detached copy of a parent production cluster, refreshed on demand.\nUseful for testing schema changes or query tuning without impacting production.\n\nConfig: \`topology-staging\``
  const hRefreshScript = `**Staging Refresh Script**\n\nPath to a script executed when the staging cluster is refreshed from the parent.\nUseful for data masking or anonymisation before the staging cluster goes online.\n\nConfig: \`topology-staging-refresh-script\``
  const hPostDetach = `**Staging Post-Detach Script**\n\nPath to a script executed after the staging cluster detaches from the parent replication stream.\n\nConfig: \`topology-staging-post-detach-script\``
  const hHeadCluster = `**Staging Multisource Head Cluster**\n\nSelects the parent cluster this staging cluster replicates from.\nUsed in multisource staging topologies.\n\nConfig: \`replication-multisource-head-clusters\``
  const hRefresh = `**Refresh Staging**\n\nTriggers an immediate refresh from the parent cluster.\n**This action overwrites all data in the staging cluster.**\n\nConfig: \`refresh-staging (action)\``
  const hBootstrap = `**Bootstrap Staging From Parent**\n\nPerforms a full reseed from the parent cluster's latest backup.\n**All existing data in the staging cluster will be replaced.**\n\nConfig: \`bootstrap-staging-from-parent (action)\``

  const dataObject = [
    { key: 'Topology Staging', help: h(hStaging, 'Topology Staging'), value: (<RMSwitch confirmTitle={'Confirm switch settings for topology-staging?'} onChange={() => dispatch(switchSetting({ clusterName: selectedCluster?.name, setting: 'topology-staging' }))} isDisabled={user?.grants['cluster-settings'] == false} isChecked={selectedCluster?.config?.topologyStaging} />) },
    { key: 'Staging Refresh Script', help: h(hRefreshScript, 'Staging Refresh Script'), value: (<TextForm value={selectedCluster?.config?.topologyStagingRefreshScript} confirmTitle={`Confirm staging refresh script to `} onSave={(v) => { dispatch(setSetting({ clusterName: selectedCluster?.name, setting: 'topology-staging-refresh-script', value: btoa(v) })) }} />) },
    { key: 'Staging Post-Detach Script', help: h(hPostDetach, 'Staging Post-Detach Script'), value: (<TextForm value={selectedCluster?.config?.topologyStagingPostDetachScript} confirmTitle={`Confirm staging post-detach script to `} onSave={(v) => { dispatch(setSetting({ clusterName: selectedCluster?.name, setting: 'topology-staging-post-detach-script', value: btoa(v) })) }} />) },
    ...(selectedCluster?.config?.topologyStaging ? [
      { key: 'Staging Multisource Head Cluster', help: h(hHeadCluster, 'Staging Multisource Head Cluster'), value: (<Dropdown id='replication-multisource-head-clusters' confirmTitle={`Confirm staging replication-multisource-head-clusters to `} onChange={(v) => { dispatch(setSetting({ clusterName: selectedCluster?.name, setting: 'replication-multisource-head-clusters', value: v })) }} selectedValue={selectedCluster?.config?.replicationMultisourceHeadClusters} options={clusters} className={styles.fullWidth} />) },
      { key: 'Refresh Staging', help: h(hRefresh, 'Refresh Staging'), value: (<RMIconButton icon={TbDatabaseExport} onClick={() => { openConfirmModal(`Confirm refresh-staging? This action can not be undone!`, () => () => { dispatch(refreshStaging({ clusterName: selectedCluster?.name })) }) }} />) },
      { key: 'Bootstrap Staging From Parent', help: h(hBootstrap, 'Bootstrap Staging From Parent'), value: (<RMIconButton icon={TbDatabaseImport} onClick={() => { openConfirmModal(`Confirm bootstrap staging from parent? Data will be overwritten by parent cluster!`, () => () => { dispatch(reseedStagingFromParent({ clusterName: selectedCluster?.name })) }) }} />) },
    ] : []),
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

export default StagingSettings
