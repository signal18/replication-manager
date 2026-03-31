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

  const helpKey = (label, content) => (
    <Box as="span" display="inline">
      {label}
      <Box as="span" display="inline-flex" verticalAlign="middle" ml={1}>
        <RMIconButton icon={HiQuestionMarkCircle} onClick={() => openInfoModal(label, content)} />
      </Box>
    </Box>
  )

  useEffect(() => {
    if (monitor?.clusters) {
      setClusters(monitor?.clusters?.filter((cl) => cl != selectedCluster.name).map((cluster) => ({ name: cluster, value: cluster })))
    }
  }, [monitor?.clusters?.join(',')])

  const helpStaging = `**Topology Staging**\n\nEnables staging mode for this cluster.\nIn staging mode the cluster acts as a detached copy of a parent production cluster, refreshed on demand.\nUseful for testing schema changes, query tuning, or training without impacting production.`
  const helpRefreshScript = `**Staging Refresh Script**\n\nPath to a script executed when the staging cluster is refreshed from the parent.\nThe script can perform custom data masking, anonymisation, or transformation steps before the staging cluster is brought online.\nReceives the cluster name as its first argument.`
  const helpPostDetach = `**Staging Post-Detach Script**\n\nPath to a script executed after the staging cluster is detached from the parent replication stream.\nUse to apply additional configuration changes, load test data, or notify downstream systems that the staging environment is ready.`
  const helpHeadCluster = `**Staging Multisource Head Cluster**\n\nSelects the parent cluster that this staging cluster replicates from.\nUsed in multisource staging topologies where a single staging cluster mirrors data from multiple production clusters.`
  const helpRefresh = `**Refresh Staging**\n\nTriggers an immediate refresh of the staging cluster from the parent.\nThis re-attaches the staging cluster to the parent replication stream, syncs it to the current state, then detaches it again.\n**This action overwrites all data in the staging cluster.**`
  const helpBootstrap = `**Bootstrap Staging From Parent**\n\nPerforms a full reseed of the staging cluster from the parent cluster's latest backup.\nUse when the staging cluster is too far behind to refresh incrementally, or when setting up a new staging environment.\n**All existing data in the staging cluster will be replaced.**`

  const dataObject = [
    { key: helpKey('Topology Staging', helpStaging), value: (<RMSwitch confirmTitle={'Confirm switch settings for topology-staging?'} onChange={() => dispatch(switchSetting({ clusterName: selectedCluster?.name, setting: 'topology-staging' }))} isDisabled={user?.grants['cluster-settings'] == false} isChecked={selectedCluster?.config?.topologyStaging} />) },
    { key: helpKey('Staging Refresh Script', helpRefreshScript), value: (<TextForm value={selectedCluster?.config?.topologyStagingRefreshScript} confirmTitle={`Confirm staging refresh script to `} onSave={(value) => { dispatch(setSetting({ clusterName: selectedCluster?.name, setting: 'topology-staging-refresh-script', value: btoa(value) })) }} />) },
    { key: helpKey('Staging Post-Detach Script', helpPostDetach), value: (<TextForm value={selectedCluster?.config?.topologyStagingPostDetachScript} confirmTitle={`Confirm staging post-detach script to `} onSave={(value) => { dispatch(setSetting({ clusterName: selectedCluster?.name, setting: 'topology-staging-post-detach-script', value: btoa(value) })) }} />) },
    ...(selectedCluster?.config?.topologyStaging ? [
      { key: helpKey('Staging Multisource Head Cluster', helpHeadCluster), value: (<Dropdown id='replication-multisource-head-clusters' confirmTitle={`Confirm staging replication-multisource-head-clusters to `} onChange={(value) => { dispatch(setSetting({ clusterName: selectedCluster?.name, setting: 'replication-multisource-head-clusters', value })) }} selectedValue={selectedCluster?.config?.replicationMultisourceHeadClusters} options={clusters} className={styles.fullWidth} />) },
      { key: helpKey('Refresh Staging', helpRefresh), value: (<RMIconButton icon={TbDatabaseExport} onClick={() => { openConfirmModal(`Confirm refresh-staging? This action can not be undone!`, () => () => { dispatch(refreshStaging({ clusterName: selectedCluster?.name })) }) }} />) },
      { key: helpKey('Bootstrap Staging From Parent', helpBootstrap), value: (<RMIconButton icon={TbDatabaseImport} onClick={() => { openConfirmModal(`Confirm bootstrap staging from parent? Data will be overwritten by parent cluster!`, () => () => { dispatch(reseedStagingFromParent({ clusterName: selectedCluster?.name })) }) }} />) },
    ] : []),
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

export default StagingSettings
