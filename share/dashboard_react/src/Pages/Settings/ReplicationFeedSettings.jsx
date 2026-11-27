import { Box, Flex, Text } from '@chakra-ui/react'
import React, { useState } from 'react'
import styles from './styles.module.scss'
import { useDispatch } from 'react-redux'
import TableType2 from '../../components/TableType2'
import { setSetting, switchSetting } from '../../redux/settingsSlice'
import TextForm from '../../components/TextForm'
import RMSwitch from '../../components/RMSwitch'
import RMButton from '../../components/RMButton'
import RMIconButton from '../../components/RMIconButton'
import CommonModal from '../../components/Modals/CommonModal'
import ConfirmModal from '../../components/Modals/ConfirmModal'
import modalStyles from '../../components/Modals/styles.module.scss'
import Markdown from 'react-markdown'
import remarkGfm from 'remark-gfm'
import { HiQuestionMarkCircle } from 'react-icons/hi'
import { clusterService } from '../../services/clusterService'

// The CDC feed into this cluster from a source cluster of another engine (CDC_SOURCES.md): its
// source, database and tables, the opt-in to create its publication and slot on the source, the
// bound of the WAL its slot keeps, and its two actions: a new initial load, and the removal of
// what it created on the source.

const FEED_PLUGIN = 'postgresql'

function ReplicationFeedSettings({ selectedCluster, user }) {
  const dispatch = useDispatch()
  const [action, setAction] = useState({ title: '', body: <></> })
  const [isCommonModalOpen, setIsCommonModalOpen] = useState(false)
  const [confirm, setConfirm] = useState('')
  const [result, setResult] = useState('')
  const conf = selectedCluster?.config || {}
  const name = selectedCluster?.name
  const readOnly = user?.grants['cluster-settings'] == false
  const noReplication = user?.grants['cluster-replication'] == false

  const openInfoModal = (title, content) => {
    setAction({ title, body: <Box className={modalStyles.infoTooltip}><Markdown remarkPlugins={[remarkGfm]}>{content}</Markdown></Box> })
    setIsCommonModalOpen(true)
  }
  const h = (content, title) => <RMIconButton icon={HiQuestionMarkCircle} onClick={() => openInfoModal(title, content)} iconFontsize='1rem' variant='ghost' style={{ opacity: 0.5, minWidth: '1.5rem', height: '1.5rem' }} />

  const hSource = `**Source Cluster**\n\nA PostgreSQL cluster of this replication-manager whose changes this cluster receives, by logical decoding (a publication and a replication slot on the source, never triggers). The feed reads the source's current master, applies to this cluster's master (InnoDB), and shows as a replication link of kind cdc. Enterprise plugin. Empty: no feed (the off switch; the feed's position and what it created on the source are kept).\n\nConfig: \`replication-plugin-feed-source-cluster\``
  const hDatabase = `**Source Database**\n\nThe PostgreSQL database the feed reads: logical decoding streams one database.\n\nConfig: \`replication-plugin-feed-database\``
  const hTables = `**Carried Tables**\n\nThe source tables carried, comma separated \`schema.table\` or \`schema.*\`. Each needs a primary key. A type without a MariaDB equivalent (interval, time with time zone, range, composite, domain, money) refuses its table, with the reason in the feed's state: nothing is converted silently.\n\nConfig: \`replication-plugin-feed-tables\``
  const hCreate = `**Create Publication and Slot**\n\nAllows the feed to create its publication and its replication slot on the source (\`repman_<cluster>\`), and to drop them again when it created them. This changes the client's database: off by default. Every statement is logged. A slot that the feed did not create is never dropped.\n\nConfig: \`replication-plugin-feed-create-source-objects\``
  const hRetained = `**Retained WAL Bound (MB)**\n\nA replication slot keeps WAL on the source's disk until the feed has applied it. Above this bound the feed drops its own slot, stops with a state, and needs a new initial load: the source's disk is protected first. Set \`max_slot_wal_keep_size\` on the source too (a state warns when it is unset).\n\nConfig: \`replication-plugin-feed-retained-max\``

  const run = async (fn, ok) => {
    try {
      const { data } = await fn()
      setResult(ok(data))
    } catch (e) {
      setResult(String(e?.response?.data || e?.message || 'Request failed'))
    }
  }

  const hEnabled = `**Feed Running**\n\nStarts and stops the feed (the replication interface's start and stop of the cdc link set the same switch). Stopped, its position and what it created on the source are kept, and it resumes from its slot when started again.\n\nConfig: \`replication-plugin-feed-enabled\``

  const dataObject = [
    { clusterSetting: 'replication-plugin-feed-enabled', key: 'Feed Running', help: h(hEnabled, 'Feed Running'), value: (<RMSwitch isChecked={conf.replicationPluginFeedEnabled} isDisabled={readOnly} confirmTitle={'Confirm switch settings for replication-plugin-feed-enabled?'} onChange={() => dispatch(switchSetting({ clusterName: name, setting: 'replication-plugin-feed-enabled' }))} />) },
    { clusterSetting: 'replication-plugin-feed-source-cluster', key: 'Source Cluster', help: h(hSource, 'Source Cluster'), value: (<TextForm value={conf.replicationPluginFeedSourceCluster} regexPattern={'^[A-Za-z0-9_.-]*$'} confirmTitle={'Confirm replication-plugin-feed-source-cluster to '} onSave={(v) => dispatch(setSetting({ clusterName: name, setting: 'replication-plugin-feed-source-cluster', value: v }))} />) },
    { clusterSetting: 'replication-plugin-feed-database', key: 'Source Database', help: h(hDatabase, 'Source Database'), value: (<TextForm value={conf.replicationPluginFeedDatabase} confirmTitle={'Confirm replication-plugin-feed-database to '} onSave={(v) => dispatch(setSetting({ clusterName: name, setting: 'replication-plugin-feed-database', value: v }))} />) },
    { clusterSetting: 'replication-plugin-feed-tables', key: 'Carried Tables', help: h(hTables, 'Carried Tables'), value: (<TextForm value={conf.replicationPluginFeedTables} regexPattern={'^([^.,\\s]+\\.[^.,\\s]+)(\\s*,\\s*[^.,\\s]+\\.[^.,\\s]+)*$'} confirmTitle={'Confirm replication-plugin-feed-tables to '} onSave={(v) => dispatch(setSetting({ clusterName: name, setting: 'replication-plugin-feed-tables', value: v }))} />) },
    { clusterSetting: 'replication-plugin-feed-create-source-objects', key: 'Create Publication and Slot', help: h(hCreate, 'Create Publication and Slot'), value: (<RMSwitch isChecked={conf.replicationPluginFeedCreateSourceObjects} isDisabled={readOnly} confirmTitle={'Allow the feed to create its publication and replication slot on the source database?'} onChange={() => dispatch(switchSetting({ clusterName: name, setting: 'replication-plugin-feed-create-source-objects' }))} />) },
    { clusterSetting: 'replication-plugin-feed-retained-max', key: 'Retained WAL Bound (MB)', help: h(hRetained, 'Retained WAL Bound'), value: (<TextForm value={String(conf.replicationPluginFeedRetainedMax ?? 10240)} regexPattern={'^[1-9][0-9]*$'} confirmTitle={'Confirm replication-plugin-feed-retained-max to '} onSave={(v) => dispatch(setSetting({ clusterName: name, setting: 'replication-plugin-feed-retained-max', value: v }))} />) }
  ]

  return (
    <>
      <Flex justify='space-between' gap='0'>
        <TableType2 dataArray={dataObject} className={styles.tableWithHelp} helpColumn={true} />
      </Flex>
      <Flex gap='2' mt='2' align='center' wrap='wrap'>
        <RMButton isDisabled={noReplication} onClick={() => setConfirm('reinit')}>New Initial Load</RMButton>
        <RMButton isDisabled={noReplication || (!!conf.replicationPluginFeedSourceCluster && conf.replicationPluginFeedEnabled)} onClick={() => setConfirm('drop')}>Drop Slot and Publication on Source</RMButton>
        {result && <Text fontSize='sm'>{result}</Text>}
      </Flex>
      {confirm && (
        <ConfirmModal
          isOpen={!!confirm}
          closeModal={() => setConfirm('')}
          title={confirm === 'reinit' ? 'Start the feed again from a new initial load? Its tables on this cluster are emptied, then copied again from the source.' : 'Drop the replication slot and the publication that the feed created on the source? Only those it created are dropped.'}
          onConfirmClick={() => {
            if (confirm === 'reinit') run(() => clusterService.reinitReplicationPlugin(name, FEED_PLUGIN), () => 'New initial load requested')
            else run(() => clusterService.dropReplicationFeedSourceObjects(name, FEED_PLUGIN), (d) => `Ran on the source: ${(d?.statements || []).join('; ') || 'nothing'}`)
            setConfirm('')
          }}
        />
      )}
      <CommonModal isOpen={isCommonModalOpen} closeModal={() => setIsCommonModalOpen(false)} title={action.title} body={action.body} size='xl' />
    </>
  )
}

export default ReplicationFeedSettings
