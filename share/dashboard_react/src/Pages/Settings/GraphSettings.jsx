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

  const h = (content, title) => (
    <RMIconButton icon={HiQuestionMarkCircle} onClick={() => openInfoModal(title, content)} />
  )

  const { globalClusters: { monitor } } = useSelector((state) => state)
  useEffect(() => { if (monitor?.graphiteTemplateList) setGraphiteTemplateOptions(convertObjectToArrayForDropdown(monitor.graphiteTemplateList)) }, [monitor?.graphiteTemplateList])

  const hMetrics = `**Graphite Metrics**\n\nEnables pushing cluster and server metrics to a Graphite server.\nRequires a Graphite server reachable from the replication-manager host.\n\nConfig: \`graphite-metrics\``
  const hEmbedded = `**Graphite Embedded**\n\nStarts an embedded Graphite-compatible server inside replication-manager.\nUseful for development or small deployments without a separate Graphite installation.\n\nConfig: \`graphite-embedded\``
  const hTemplate = `**Reset Graphite Template**\n\nResets the Graphite metric filter list to the selected built-in template.\nThis overwrites any custom whitelist or blacklist currently configured.\n\nConfig: \`reset-graphite-filterlist\``
  const hWhitelist = `**Graphite Whitelist**\n\nRegular expression list (one per line) of metric names to **include** when forwarding to Graphite.\nLeave empty to forward all metrics (subject to the blacklist).\n\nConfig: \`graphite-whitelist\``
  const hBlacklist = `**Graphite Blacklist**\n\nRegular expression list (one per line) of metric names to **exclude** when forwarding to Graphite.\nMetrics matching any blacklist entry are dropped even if they match the whitelist.\n\nConfig: \`graphite-blacklist\``

  const dataObject = [
    {
      key: 'GRAPHITE CONFIG', value: [
        { key: 'Graphite Metrics', help: h(hMetrics, 'Graphite Metrics'), value: (<RMSwitch confirmTitle={'Confirm switch settings for graphite-metrics?'} onChange={() => dispatch(switchSetting({ clusterName: selectedCluster?.name, setting: 'graphite-metrics' }))} isDisabled={user?.grants['cluster-settings'] == false} isChecked={selectedCluster?.config?.graphiteMetrics} />) },
        { key: 'Graphite Embedded', help: h(hEmbedded, 'Graphite Embedded'), value: (<RMSwitch confirmTitle={'Confirm switch settings for graphite-embedded?'} onChange={() => dispatch(switchSetting({ clusterName: selectedCluster?.name, setting: 'graphite-embedded' }))} isDisabled={user?.grants['cluster-settings'] == false} isChecked={selectedCluster?.config?.graphiteEmbedded} />) },
      ]
    },
    {
      key: 'METRICS CONFIGURATION', value: [
        { key: 'Reset Graphite Template', help: h(hTemplate, 'Reset Graphite Template'), value: (<Dropdown options={graphiteTemplateOptions} selectedValue={selectedCluster?.config?.graphiteWhitelistTemplate} confirmTitle={`Confirm reset graphite filterlist to `} onChange={(v) => dispatch(setSetting({ clusterName: selectedCluster?.name, setting: 'reset-graphite-filterlist', value: v }))} />) },
        { key: 'Graphite Whitelist', help: h(hWhitelist, 'Graphite Whitelist'), value: (<RegexText user={user} value={selectedCluster?.Whitelist?.join('\n')} isSwitchChecked={selectedCluster?.config?.graphiteWhitelist} switchConfirmTitle={'Confirm switch settings for graphite-whitelist?'} onSwitchChange={() => dispatch(switchSetting({ clusterName: selectedCluster?.name, setting: 'graphite-whitelist' }))} confirmTitle={'Confirm update graphite whitelist?'} onSave={(v) => dispatch(updateGraphiteWhiteList({ clusterName: selectedCluster?.name, whiteListValue: v }))} />) },
        { key: 'Graphite Blacklist', help: h(hBlacklist, 'Graphite Blacklist'), value: (<RegexText user={user} value={selectedCluster?.Blacklist?.join('\n')} isSwitchChecked={selectedCluster?.config?.graphiteBlacklist} switchConfirmTitle={'Confirm switch settings for graphite-blacklist?'} onSwitchChange={() => dispatch(switchSetting({ clusterName: selectedCluster?.name, setting: 'graphite-blacklist' }))} confirmTitle={'Confirm update graphite Blacklist?'} onSave={(v) => dispatch(updateGraphiteBlackList({ clusterName: selectedCluster?.name, blackListValue: v }))} />) },
      ]
    }
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

export default GraphSettings
