import React, { useCallback, useEffect, useState } from 'react'
import PropTypes from 'prop-types'
import { Box, Text } from '@chakra-ui/react'
import Dropdown from '../../components/Dropdown'
import { clusterService } from '../../services/clusterService'
import { ATTACH_NONE, attachArchiveRequest, attachConfirmBody, attachOptionsOf, kindLabel, sizeText, summary } from '../Settings/ArchiveListSettings'
import styles from './styles.module.scss'

// The attached archive in the Maintenance backup section (#2091): the archive the backups go
// to, where it is, its size and the backups it holds, and a pick to change it, as in the
// Archive settings. onAttached gets the attached archive (null for none) at each read.
function AttachedArchive({ clusterName, user, onAttached }) {
  const [list, setList] = useState({ attached: '', archives: [], plugins: [] })
  const [error, setError] = useState('')
  const canSet = user?.grants?.['cluster-settings'] !== false

  const fetchList = useCallback(async () => {
    if (!clusterName) return
    try {
      const { data, status } = await clusterService.getArchiveList(clusterName)
      if (status >= 200 && status < 300 && data) {
        const next = { attached: data.attached || '', archives: data.archives || [], plugins: data.plugins || [] }
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
  return (
    <>
      <Box className={styles.repoRow}>
        <Box className={styles.repoRowLabel}>Attached archive:</Box>
        <Box className={styles.repoRowValue}>
          <Dropdown options={attachOptionsOf(list.archives)} selectedValue={list.attached || ATTACH_NONE} isDisabled={!canSet}
            confirmTitle='Change the attached archive?' confirmBody={attachConfirmBody(list.attached)} onChange={change} />
        </Box>
      </Box>
      {archive ? (
        <Box className={styles.repoRow}>
          <Box className={styles.repoRowLabel}>{kindLabel(archive.plugin, manifest)}:</Box>
          <Box className={styles.repoRowValue}>
            {archive.location || summary(archive)} · {sizeText(archive)}{archive.backups != null ? ` · ${archive.backups} backup${archive.backups === 1 ? '' : 's'}` : ''}
          </Box>
        </Box>
      ) : (
        <Text fontSize='sm'>No archive attached: the backups stay in the servers&apos; backup folders.</Text>
      )}
      {error && <Text color='red.500' fontSize='sm'>{error}</Text>}
    </>
  )
}

AttachedArchive.propTypes = {
  clusterName: PropTypes.string,
  user: PropTypes.object,
  onAttached: PropTypes.func
}

export default AttachedArchive
