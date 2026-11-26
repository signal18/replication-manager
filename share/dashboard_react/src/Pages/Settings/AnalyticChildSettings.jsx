import { Box, Flex, Text } from '@chakra-ui/react'
import React, { useState } from 'react'
import styles from './styles.module.scss'
import { useDispatch, useSelector } from 'react-redux'
import TableType2 from '../../components/TableType2'
import { setSetting } from '../../redux/settingsSlice'
import TextForm from '../../components/TextForm'
import RMIconButton from '../../components/RMIconButton'
import CommonModal from '../../components/Modals/CommonModal'
import modalStyles from '../../components/Modals/styles.module.scss'
import Markdown from 'react-markdown'
import remarkGfm from 'remark-gfm'
import { HiQuestionMarkCircle } from 'react-icons/hi'
import AnalyticChildIndexes from './AnalyticChildIndexes'

// The analytic child (CDC_CORE.md): its destination plugin and tables, the CDC core settings
// of a ColumnStore or analytic child (its source, checkpoint and merge intervals), and the
// indexes of its denormalize mode.

// a carried table of this many rows or more: a merge interval of an hour costs far less than
// applying each checkpoint to a destination without indexes (each key batch scans the table)
const MERGE_HINT_ROWS = 3000000

// selects: replication-plugin-tables (schema.table or schema.*, comma separated) takes schema.table
export function analyticChildSelects(patterns, schema, table) {
  return (patterns || '').split(',').some((p) => {
    const [s, t] = p.trim().split('.')
    return s && t && s.toLowerCase() === (schema || '').toLowerCase() && (t === '*' || t.toLowerCase() === (table || '').toLowerCase())
  })
}

function AnalyticChildSettings({ selectedCluster, user }) {
  const dispatch = useDispatch()
  const schema = useSelector((state) => state.cluster?.shardSchema)
  const [action, setAction] = useState({ title: '', body: <></> })
  const [isCommonModalOpen, setIsCommonModalOpen] = useState(false)
  const conf = selectedCluster?.config || {}
  const name = selectedCluster?.name
  const readOnly = user?.grants['cluster-settings'] == false

  const openInfoModal = (title, content) => {
    setAction({ title, body: <Box className={modalStyles.infoTooltip}><Markdown remarkPlugins={[remarkGfm]}>{content}</Markdown></Box> })
    setIsCommonModalOpen(true)
  }
  const h = (content, title) => <RMIconButton icon={HiQuestionMarkCircle} onClick={() => openInfoModal(title, content)} iconFontsize='1rem' variant='ghost' style={{ opacity: 0.5, minWidth: '1.5rem', height: '1.5rem' }} />

  const big = (Array.isArray(schema) ? schema : []).filter((t) => (t?.table_rows || 0) >= MERGE_HINT_ROWS && analyticChildSelects(conf.replicationPluginTables, t.table_schema, t.table_name))

  const hDestination = `**Destination Plugin**\n\nMakes this child cluster an analytic child: the destination plugin (\`replplugin-<name>\`, installed with replication-manager) makes the copies of the tables listed in Copied Tables in its engine, at the next seed from the parent, on this cluster only (never on the parent), and feeds them from the parent's binlog.\nA table the destination cannot hold stays as it is, replicated natively, with a schema state giving the reason.\nAdds the configurator tags the plugin's manifest names. An enterprise plugin needs an enterprise plan. Empty: none.\n\nConfig: \`replication-plugin-destination\``
  const hTables = `**Copied Tables**\n\nThe tables the destination plugin copies, comma separated: \`schema.table\` or \`schema.*\`. Read at the next seed from the parent.\n\nConfig: \`replication-plugin-tables\``
  const hSource = `**Replication Plugin Source**\n\nThe parent server whose binlog the replication plugin reads.\n- \`master\`: the parent's master, followed after a failover or a switchover.\n- \`host:port\`: a dedicated replica of the parent (binlog_format ROW, binlog_row_image FULL, log_slave_updates ON, GTID). If it is lost, the plugin pauses with a state: it never moves to another server by itself.\n\nConfig: \`replication-plugin-source\``
  const hCheckpoint = `**Checkpoint Interval**\n\nSeconds of the parent's binlog per checkpoint of the replication plugin: the freshness of the copies when the merge interval is 0.\n\nConfig: \`replication-plugin-checkpoint-interval\``
  const hMerge = `**Merge Interval**\n\nYour choice of freshness against cost.\n0: each checkpoint is applied to the copies (freshness of a few seconds).\nMore: each checkpoint goes to an InnoDB staging on this cluster, merged into the copies every this many seconds.\nOn a destination without indexes every batch of keys of a checkpoint scans its table: for tables of a few million rows or more, or a busy parent, 3600 (one hour) costs far less.\n\nConfig: \`replication-plugin-merge-interval\``

  const dataObject = [
    { clusterSetting: 'replication-plugin-destination', key: 'Destination Plugin', help: h(hDestination, 'Destination Plugin'), value: (<TextForm value={conf.replicationPluginDestination} regexPattern={'^[A-Za-z0-9_-]*$'} confirmTitle={'Confirm replication-plugin-destination to '} onSave={(v) => dispatch(setSetting({ clusterName: name, setting: 'replication-plugin-destination', value: v }))} />) },
    { clusterSetting: 'replication-plugin-tables', key: 'Copied Tables', help: h(hTables, 'Copied Tables'), value: (<TextForm value={conf.replicationPluginTables} regexPattern={'^([^.,\\s]+\\.[^.,\\s]+)(\\s*,\\s*[^.,\\s]+\\.[^.,\\s]+)*$'} confirmTitle={'Confirm replication-plugin-tables to '} onSave={(v) => dispatch(setSetting({ clusterName: name, setting: 'replication-plugin-tables', value: v }))} />) },
    { clusterSetting: 'replication-plugin-source', key: 'Replication Plugin Source', help: h(hSource, 'Replication Plugin Source'), value: (<TextForm value={conf.replicationPluginSource || 'master'} confirmTitle={'Confirm replication-plugin-source to '} onSave={(v) => dispatch(setSetting({ clusterName: name, setting: 'replication-plugin-source', value: v }))} />) },
    { clusterSetting: 'replication-plugin-checkpoint-interval', key: 'Checkpoint Interval (s)', help: h(hCheckpoint, 'Checkpoint Interval'), value: (<TextForm value={String(conf.replicationPluginCheckpointInterval ?? 10)} regexPattern={'^[1-9][0-9]*$'} confirmTitle={'Confirm replication-plugin-checkpoint-interval to '} onSave={(v) => dispatch(setSetting({ clusterName: name, setting: 'replication-plugin-checkpoint-interval', value: v }))} />) },
    {
      clusterSetting: 'replication-plugin-merge-interval',
      key: 'Merge Interval (s)',
      help: h(hMerge, 'Merge Interval'),
      value: (
        <Box>
          <TextForm value={String(conf.replicationPluginMergeInterval ?? 0)} regexPattern={'^[0-9]+$'} confirmTitle={'Confirm replication-plugin-merge-interval to '} onSave={(v) => dispatch(setSetting({ clusterName: name, setting: 'replication-plugin-merge-interval', value: v }))} />
          {conf.replicationPluginDestination && !conf.replicationPluginMergeInterval && big.length > 0 && (
            <Text fontSize='sm' color='orange.500'>
              {`${big.map((t) => `${t.table_schema}.${t.table_name}`).join(', ')}: over ${MERGE_HINT_ROWS / 1000000} million rows, a merge interval of 3600 (one hour) costs far less than applying each checkpoint`}
            </Text>
          )}
        </Box>
      )
    }
  ]

  return (
    <>
      <Flex justify='space-between' gap='0'>
        <TableType2 dataArray={dataObject} className={styles.tableWithHelp} helpColumn={true} />
      </Flex>
      {conf.replicationPluginDestination && <AnalyticChildIndexes clusterName={name} readOnly={readOnly} />}
      <CommonModal isOpen={isCommonModalOpen} closeModal={() => setIsCommonModalOpen(false)} title={action.title} body={action.body} size='xl' />
    </>
  )
}

export default AnalyticChildSettings
