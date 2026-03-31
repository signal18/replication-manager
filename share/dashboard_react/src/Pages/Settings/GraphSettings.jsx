import { Box, Flex } from '@chakra-ui/react'
import React, { useEffect, useState } from 'react'
import styles from './styles.module.scss'
import RMSwitch from '../../components/RMSwitch'
import { useDispatch, useSelector } from 'react-redux'
import TableType2 from '../../components/TableType2'
import { setSetting, switchSetting, updateGraphiteBlackList, updateGraphiteWhiteList } from '../../redux/settingsSlice'
import Dropdown from '../../components/Dropdown'
import { convertObjectToArrayForDropdown } from '../../utility/common'
import RegexText from './RegexText'
import CommonModal from '../../components/Modals/CommonModal'
import modalStyles from '../../components/Modals/styles.module.scss'
import Markdown from 'react-markdown'
import remarkGfm from 'remark-gfm'
import { HiQuestionMarkCircle } from 'react-icons/hi'
import RMIconButton from '../../components/RMIconButton'

function GraphSettings({ selectedCluster, user, openConfirmModal }) {
  const dispatch = useDispatch()
  const [graphiteTemplateOptions, setGraphiteTemplateOptions] = useState()
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

  const { globalClusters: { monitor } } = useSelector((state) => state)

  useEffect(() => {
    if (monitor?.graphiteTemplateList) {
      setGraphiteTemplateOptions(convertObjectToArrayForDropdown(monitor.graphiteTemplateList))
    }
  }, [monitor?.graphiteTemplateList])

  const helpMetrics = `**Graphite Metrics**\n\nEnables pushing cluster and server metrics to a Graphite server.\nMetrics include replication lag, query throughput, InnoDB buffer pool usage, and all collected Performance Schema counters.\nRequires a Graphite server to be reachable from the replication-manager host.`
  const helpEmbedded = `**Graphite Embedded**\n\nStarts an embedded Graphite-compatible server inside replication-manager.\nUseful for development or small deployments where a separate Graphite installation is not available.\nData is stored in-process and not persisted across restarts.`
  const helpTemplate = `**Reset Graphite Template**\n\nResets the Graphite metric filter list to the selected built-in template.\nTemplates define which metrics are forwarded to Graphite.\nThis overwrites any custom whitelist or blacklist currently configured.`
  const helpWhitelist = `**Graphite Whitelist**\n\nRegular expression list (one per line) of metric names to **include** when forwarding to Graphite.\nOnly metrics matching at least one whitelist entry are sent.\nLeave empty to forward all metrics (subject to the blacklist).`
  const helpBlacklist = `**Graphite Blacklist**\n\nRegular expression list (one per line) of metric names to **exclude** when forwarding to Graphite.\nMetrics matching any blacklist entry are dropped even if they match the whitelist.`

  const dataObject = [
    {
      key: 'GRAPHITE CONFIG',
      value: [
        { key: helpKey('Graphite Metrics', helpMetrics), value: (<RMSwitch confirmTitle={'Confirm switch settings for graphite-metrics?'} onChange={() => dispatch(switchSetting({ clusterName: selectedCluster?.name, setting: 'graphite-metrics' }))} isDisabled={user?.grants['cluster-settings'] == false} isChecked={selectedCluster?.config?.graphiteMetrics} />) },
        { key: helpKey('Graphite Embedded', helpEmbedded), value: (<RMSwitch confirmTitle={'Confirm switch settings for graphite-embedded?'} onChange={() => dispatch(switchSetting({ clusterName: selectedCluster?.name, setting: 'graphite-embedded' }))} isDisabled={user?.grants['cluster-settings'] == false} isChecked={selectedCluster?.config?.graphiteEmbedded} />) }
      ]
    },
    {
      key: 'METRICS CONFIGURATION',
      value: [
        { key: helpKey('Reset Graphite Template', helpTemplate), value: (<Dropdown options={graphiteTemplateOptions} selectedValue={selectedCluster?.config?.graphiteWhitelistTemplate} confirmTitle={`Confirm reset graphite filterlist to `} onChange={(value) => dispatch(setSetting({ clusterName: selectedCluster?.name, setting: 'reset-graphite-filterlist', value }))} />) },
        { key: helpKey('Graphite Whitelist', helpWhitelist), value: (<RegexText user={user} value={selectedCluster?.Whitelist?.join('\n')} isSwitchChecked={selectedCluster?.config?.graphiteWhitelist} switchConfirmTitle={'Confirm switch settings for graphite-whitelist?'} onSwitchChange={() => dispatch(switchSetting({ clusterName: selectedCluster?.name, setting: 'graphite-whitelist' }))} confirmTitle={'Confirm update graphite whitelist?'} onSave={(value) => dispatch(updateGraphiteWhiteList({ clusterName: selectedCluster?.name, whiteListValue: value }))} />) },
        { key: helpKey('Graphite Blacklist', helpBlacklist), value: (<RegexText user={user} value={selectedCluster?.Blacklist?.join('\n')} isSwitchChecked={selectedCluster?.config?.graphiteBlacklist} switchConfirmTitle={'Confirm switch settings for graphite-blacklist?'} onSwitchChange={() => dispatch(switchSetting({ clusterName: selectedCluster?.name, setting: 'graphite-blacklist' }))} confirmTitle={'Confirm update graphite Blacklist?'} onSave={(value) => dispatch(updateGraphiteBlackList({ clusterName: selectedCluster?.name, blackListValue: value }))} />) }
      ]
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

export default GraphSettings
