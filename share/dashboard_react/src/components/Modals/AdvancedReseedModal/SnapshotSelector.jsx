import React from 'react'
import { Badge, HStack, Select, Text } from '@chakra-ui/react'
import {
  extractServerInfoFromPath,
  formatLocalDateTime,
  formatMetadataTimestamp,
  getPreferredMetadata,
  parseSnapshotTags
} from './helpers'

function SnapshotSelector({ snapshots, selectedSnapshot, setSelectedSnapshot, operationType, theme, filterLabel }) {
  const formattedTimesById = new Map()
  const dedupedSnapshots = []
  const seenTimes = new Set()
  const operationMethod = operationType === 'physical-backup' ? 'physical' : 'logical'

  snapshots.forEach((snapshot) => {
    const preferredMeta = getPreferredMetadata(snapshot, operationType)
    const formattedTime =
      formatMetadataTimestamp(preferredMeta, formatLocalDateTime) || formatLocalDateTime(snapshot.time)
    formattedTimesById.set(snapshot.id, formattedTime)

    const tagMeta = parseSnapshotTags(snapshot.tags || [])
    const normalizedTagBackupType = tagMeta.backupType ? tagMeta.backupType.toLowerCase() : null
    const tagMethodMatches = normalizedTagBackupType === operationMethod
    const metadataMethodMatches = preferredMeta?.backupMethod?.toLowerCase() === operationMethod

    const shouldKeepByMethod = tagMethodMatches
    const shouldKeepByMetadata = !tagMethodMatches && metadataMethodMatches && Boolean(formattedTime)
    const shouldKeepWithoutTimestamp = !formattedTime

    if (!(shouldKeepByMethod || shouldKeepByMetadata || shouldKeepWithoutTimestamp)) {
      return
    }

    if (formattedTime) {
      if (seenTimes.has(formattedTime)) {
        return
      }
      seenTimes.add(formattedTime)
    }

    dedupedSnapshots.push(snapshot)
  })

  return (
    <>
      <HStack spacing={2} mb={2}>
        <Badge colorScheme='green' variant='subtle' fontSize='0.65rem'>
          latest
        </Badge>
        <Text fontSize='xs' color={theme === 'light' ? 'gray.600' : 'gray.300'}>
          Showing the most recent snapshot per backup session ({filterLabel})
        </Text>
      </HStack>
      <Select
        value={selectedSnapshot}
        onChange={(e) => setSelectedSnapshot(e.target.value)}
        placeholder='Select a snapshot'
        mb={3}
      >
        {dedupedSnapshots.map((snapshot) => {
          const preferredMeta = getPreferredMetadata(snapshot, operationType)
          const formattedTime =
            formattedTimesById.get(snapshot.id) ||
            formatMetadataTimestamp(preferredMeta, formatLocalDateTime) ||
            formatLocalDateTime(snapshot.time)
          const hasFormattedTime = Boolean(formattedTime)
          const primaryPath = snapshot.paths?.[0] || ''
          const { clusterName, serverHost, serverPort, isAdhoc, backupTool } = extractServerInfoFromPath(
            primaryPath,
            snapshot.tags || []
          )
          const tagMeta = parseSnapshotTags(snapshot.tags || [])
          let displayText = `${snapshot.short_id}`
          if (hasFormattedTime) {
            displayText += ` - ${formattedTime}`
          }
          displayText += ` - ${clusterName} - ${serverHost}:${serverPort}`
          if (isAdhoc && backupTool) {
            displayText += ` (adhoc:${backupTool})`
          }
          if (preferredMeta?.backupMethod && preferredMeta?.backupMethod !== tagMeta.backupType) {
            displayText += ` (${preferredMeta.backupMethod})`
          }
          if (tagMeta.status === 'available') {
            displayText += ' [latest]'
          } else if (tagMeta.status === 'orphaned') {
            displayText += ' [orphaned]'
          }
          if (!snapshot.metadataReady) {
            displayText += ' [metadata pending]'
          }

          return (
            <option key={snapshot.id} value={snapshot.id}>
              {displayText}
            </option>
          )
        })}
      </Select>
    </>
  )
}

export default SnapshotSelector
