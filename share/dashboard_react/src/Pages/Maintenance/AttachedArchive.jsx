import React, { useCallback, useEffect, useMemo, useState } from 'react'
import { createColumnHelper } from '@tanstack/react-table'
import { HiRefresh, HiRewind, HiTrash } from 'react-icons/hi'
import PropTypes from 'prop-types'
import { Box, Text } from '@chakra-ui/react'
import RMButton from '../../components/RMButton'
import Dropdown from '../../components/Dropdown'
import { DataTable } from '../../components/DataTable'
import RMIconButton from '../../components/RMIconButton'
import ConfirmModal from '../../components/Modals/ConfirmModal'
import { formatBytes } from '../../utility/common'
import { clusterService } from '../../services/clusterService'
import { ATTACH_NONE, attachArchiveRequest, attachConfirmBody, attachOptionsOf, kindLabel, sizeText, summary } from '../Settings/ArchiveListSettings'
import styles from './styles.module.scss'

// The attached archive in the Maintenance backup section (#2091): the archive the backups go
// to, where it is, its size and the backups it holds, and a pick to change it, as in the
// Archive settings. onAttached gets the attached archive (null for none) at each read. An
// archive plugin's archive lists its snapshots here; a restic archive's are listed below it.
const columnHelper = createColumnHelper()
const isRestic = (a) => a?.plugin?.startsWith('archive-restic-')

function AttachedArchive({ clusterName, user, onAttached, snapbackAsk, onSnapbackAskDone }) {
  const [list, setList] = useState({ attached: '', archives: [], plugins: [], snapbacks: [] })
  const [error, setError] = useState('')
  const [snapshots, setSnapshots] = useState({ archive: '', rows: [], error: '', loading: false })
  const [forgetId, setForgetId] = useState('') // the snapshot asked for deletion
  const [snapbackSnap, setSnapbackSnap] = useState(null) // the snapshot asked for a snapback: { id, path }
  const canSet = user?.grants?.['cluster-settings'] !== false
  const canDelete = user?.grants?.['db-backup'] !== false
  const canRestore = user?.grants?.['db-restore'] !== false

  const fetchList = useCallback(async () => {
    if (!clusterName) return
    try {
      const { data, status } = await clusterService.getArchiveList(clusterName)
      if (status >= 200 && status < 300 && data) {
        const next = { attached: data.attached || '', archives: data.archives || [], plugins: data.plugins || [], snapbacks: data.snapbacks || [] }
        setList(next)
        onAttached?.(next.archives.find((a) => a.name === next.attached) || null)
      } else {
        setError(typeof data === 'string' ? data : `Failed to list the archives (status ${status})`)
      }
    } catch (e) {
      setError(e?.message || 'Failed to list the archives')
    }
  }, [clusterName, onAttached])

  useEffect(() => { fetchList() }, [fetchList])

  // a snapback runs in the background: follow it until it ends
  const snapbackRunning = list.snapbacks.some((s) => s.status === 'running')
  useEffect(() => {
    if (!snapbackRunning) return undefined
    const t = setInterval(fetchList, 3000)
    return () => clearInterval(t)
  }, [snapbackRunning, fetchList])

  // a snapback asked from the restic snapshots below
  useEffect(() => {
    if (snapbackAsk?.id) setSnapbackSnap(snapbackAsk)
  }, [snapbackAsk])

  const change = async (value) => {
    setError('')
    try {
      const { data, status } = await attachArchiveRequest(clusterName, value)
      if (status < 200 || status >= 300) {
        setError(typeof data === 'string' ? data.trim() : `status ${status}`)
      }
    } catch (e) {
      setError(e?.message || 'Request failed')
    }
    fetchList()
  }

  const archive = list.archives.find((a) => a.name === list.attached)
  const manifest = archive && list.plugins.find((p) => p.name === archive.plugin)
  const pluginArchive = archive && !isRestic(archive) ? archive.name : ''

  const fetchSnapshots = useCallback(async () => {
    if (!clusterName || !pluginArchive) return
    setSnapshots((s) => ({ ...s, archive: pluginArchive, loading: true, error: '' }))
    try {
      const { data, status } = await clusterService.getArchiveSnapshots(clusterName, pluginArchive)
      if (status >= 200 && status < 300 && Array.isArray(data)) {
        setSnapshots({ archive: pluginArchive, rows: data, error: '', loading: false })
      } else {
        setSnapshots({ archive: pluginArchive, rows: [], error: typeof data === 'string' ? data.trim() : `status ${status}`, loading: false })
      }
    } catch (e) {
      setSnapshots({ archive: pluginArchive, rows: [], error: e?.message || 'Failed to list the snapshots', loading: false })
    }
  }, [clusterName, pluginArchive])

  useEffect(() => { fetchSnapshots() }, [fetchSnapshots])

  const snapshotColumns = useMemo(() => [
    columnHelper.accessor((row) => (row.id || '').slice(0, 8), { header: 'ID', id: 'id' }),
    columnHelper.accessor((row) => (row.time ? new Date(row.time).toLocaleString() : ''), { header: 'Time', id: 'time' }),
    columnHelper.accessor((row) => row.paths?.join(','), { header: 'Path', id: 'paths' }),
    columnHelper.accessor((row) => row.host, { header: 'Hostname', id: 'host' }),
    columnHelper.accessor((row) => row.tags?.join(','), { header: 'Tags', id: 'tags' }),
    columnHelper.accessor((row) => (row.size ? formatBytes(row.size) : ''), { header: 'Size', id: 'size' }),
    columnHelper.display({
      id: 'actions',
      header: 'Actions',
      cell: (info) => (
        <>
          <RMIconButton icon={HiRewind} tooltip='Snapback: put the backup directory back as it is in this snapshot' isDisabled={!canRestore}
            onClick={() => setSnapbackSnap({ id: info.row.original.id, path: info.row.original.paths?.[0] || '' })} />
          <RMIconButton icon={HiTrash} tooltip='Delete snapshot' isDisabled={!canDelete}
            onClick={() => setForgetId(info.row.original.id)} />
        </>
      )
    })
  ], [canDelete, canRestore])

  const closeSnapback = () => {
    setSnapbackSnap(null)
    onSnapbackAskDone?.()
  }
  const snapback = async () => {
    const id = snapbackSnap?.id
    closeSnapback()
    try {
      const { data, status } = await clusterService.snapbackArchive(clusterName, id)
      if (status < 200 || status >= 300) setError(typeof data === 'string' ? data.trim() : `status ${status}`)
    } catch (e) {
      setError(e?.message || 'Snapback failed')
    }
    fetchList()
  }
  const release = async (server) => {
    try {
      const { data, status } = await clusterService.releaseSnapback(clusterName, server)
      if (status < 200 || status >= 300) setError(typeof data === 'string' ? data.trim() : `status ${status}`)
    } catch (e) {
      setError(e?.message || 'Release failed')
    }
    fetchList()
  }
  const snapbackLine = (s) => {
    if (s.status === 'running') return `being put back to snapshot ${s.snapshot.slice(0, 8)}: its backups wait`
    if (s.status === 'failed') return `snapback to ${s.snapshot.slice(0, 8)} failed: ${s.error}`
    return `at snapshot ${s.snapshot.slice(0, 8)}, its latest is ${s.head.slice(0, 8)}: its backups are locked until a snapback to ${s.head.slice(0, 8)} or a release`
  }

  const forget = async () => {
    const id = forgetId
    setForgetId('')
    try {
      const { data, status } = await clusterService.forgetArchiveSnapshot(clusterName, pluginArchive, id)
      if (status < 200 || status >= 300) {
        setSnapshots((s) => ({ ...s, error: typeof data === 'string' ? data.trim() : `status ${status}` }))
        return
      }
    } catch (e) {
      setSnapshots((s) => ({ ...s, error: e?.message || 'Delete failed' }))
      return
    }
    fetchSnapshots()
    fetchList()
  }
  return (
    <>
      {/* one row: the pick, what the archive is and holds, and its snapshots' count */}
      <Box className={styles.repoRow}>
        <Box className={styles.repoRowLabel}>Attached archive:</Box>
        <Dropdown options={attachOptionsOf(list.archives)} selectedValue={list.attached || ATTACH_NONE} isDisabled={!canSet}
          confirmTitle='Change the attached archive?' confirmBody={attachConfirmBody(list.attached)} onChange={change} />
        {archive ? (
          <>
            <Box className={styles.repoRowLabel}>{kindLabel(archive.plugin, manifest)}</Box>
            <Box className={styles.repoRowValue}>
              {archive.location || summary(archive)} · {sizeText(archive)}{archive.backups != null ? ` · ${archive.backups} backup${archive.backups === 1 ? '' : 's'}` : ''}
              {pluginArchive && !snapshots.loading && !snapshots.error ? ` · ${snapshots.rows.length} snapshot${snapshots.rows.length === 1 ? '' : 's'}` : ''}
            </Box>
            {pluginArchive && <RMIconButton icon={HiRefresh} tooltip='Read the snapshots again' isDisabled={snapshots.loading} onClick={fetchSnapshots} />}
          </>
        ) : (
          <Text fontSize='sm' fontWeight='normal'>none: the backups stay in the servers&apos; backup folders</Text>
        )}
        {error && <Text color='red.500' fontSize='sm'>{error}</Text>}
      </Box>
      {list.snapbacks.map((s) => (
        <Box key={s.server} className={styles.repoRow}>
          <Box className={styles.repoRowLabel}>Snapback:</Box>
          <Box className={styles.repoRowValue} color={s.status === 'failed' ? 'red.500' : undefined}>{s.server} {snapbackLine(s)}</Box>
          {s.status !== 'running' && (
            <RMButton size='xs' variant='outline' isDisabled={!canRestore} onClick={() => release(s.server)}
              title='Keep the backup directory as it is now: its next backup is its latest snapshot'>Release</RMButton>
          )}
        </Box>
      ))}
      {pluginArchive && (
        <>
          {snapshots.error
            ? <Text color='red.500' fontSize='sm'>{snapshots.error}</Text>
            : <DataTable key='archiveSnapshots' data={snapshots.rows} columns={snapshotColumns} className={styles.table} />}
        </>
      )}
      {snapbackSnap && <ConfirmModal title={`Snapback to snapshot ${snapbackSnap.id.slice(0, 8)}?`} isOpen={!!snapbackSnap}
        body={`The backup directory ${snapbackSnap.path || ''} is put back as it is in this snapshot of ${list.attached}: first the directory as it is now is snapshotted (its latest snapshot), then emptied, then this snapshot is restored into it. Until a snapback to its latest snapshot or a release, the server's backups are locked.`}
        onConfirmClick={snapback} closeModal={closeSnapback} />}
      {forgetId && <ConfirmModal title={`Delete snapshot ${forgetId.slice(0, 8)} of ${pluginArchive}?`} isOpen={!!forgetId}
        body='The snapshot is deleted from the archive at once: the backup it holds can no longer be restored from this archive. This cannot be undone.'
        onConfirmClick={forget} closeModal={() => setForgetId('')} />}
    </>
  )
}

AttachedArchive.propTypes = {
  clusterName: PropTypes.string,
  user: PropTypes.object,
  onAttached: PropTypes.func,
  snapbackAsk: PropTypes.object,
  onSnapbackAskDone: PropTypes.func
}

export default AttachedArchive
