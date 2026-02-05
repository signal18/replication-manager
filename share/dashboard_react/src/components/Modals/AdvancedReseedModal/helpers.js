const OPERATION_METHOD_MAP = {
  'logical-backup': 'logical',
  'logical-master': 'logical',
  'physical-backup': 'physical'
}

const getSnapshotMetadataByMethod = (snapshot, method) => {
  if (!snapshot?.metadata || !method) {
    return null
  }
  const normalized = method.toLowerCase()
  return snapshot.metadata.find((meta) => meta?.backupMethod?.toLowerCase() === normalized) || null
}

const getPreferredMetadata = (snapshot, operationType) => {
  const preferredMethod = OPERATION_METHOD_MAP[operationType] || 'logical'
  const fallbackMethod = preferredMethod === 'logical' ? 'physical' : 'logical'
  return getSnapshotMetadataByMethod(snapshot, preferredMethod) || getSnapshotMetadataByMethod(snapshot, fallbackMethod)
}

const formatMetadataTimestamp = (meta, formatter) => {
  if (!meta) {
    return null
  }
  if (meta.startTime) {
    return formatter(meta.startTime)
  }
  if (meta.endTime) {
    return formatter(meta.endTime)
  }
  return null
}

const parseSnapshotTags = (tags = []) => {
  const meta = {
    sessionId: null,
    backupType: null,
    backupTool: null,
    status: 'legacy',
    isLatestView: false
  }

  tags.forEach((tagRaw) => {
    if (typeof tagRaw !== 'string') {
      return
    }
    const tag = tagRaw.trim()
    const normalized = tag.toLowerCase()

    if (tag.startsWith('session:')) {
      meta.sessionId = tag.substring('session:'.length)
      meta.isLatestView = true
    } else if (tag.startsWith('backup-type:')) {
      meta.backupType = tag.substring('backup-type:'.length)
    } else if (tag.startsWith('backup-tool:')) {
      meta.backupTool = tag.substring('backup-tool:'.length)
    }

    if (normalized === 'status:orphaned' || normalized === 'state:orphaned' || normalized === 'orphaned') {
      meta.status = 'orphaned'
    }
  })

  if (meta.status !== 'orphaned' && meta.sessionId) {
    meta.status = 'available'
  }

  return meta
}

const extractServerInfoFromPath = (path, tags = []) => {
  try {
    if (!path) {
      return {
        clusterName: 'N/A',
        serverHost: 'N/A',
        serverPort: 'N/A',
        isAdhoc: false,
        backupTool: null,
        epoch: null
      }
    }

    const segments = path.split('/')
    if (segments.length < 2) {
      return {
        clusterName: 'N/A',
        serverHost: 'N/A',
        serverPort: 'N/A',
        isAdhoc: false,
        backupTool: null,
        epoch: null
      }
    }

    const isAdhocTagged = tags.some((tag) => tag === 'adhoc' || tag.includes('line:adhoc'))
    const lastSegment = segments[segments.length - 1]

    const mysqldumpPattern = /^mysqldump\.(\d+)\.sql\.gz$/
    const dumplingPattern = /^dumpling\.(\d+)$/
    const mydumperPattern = /^mydumper\.(\d+)$/

    let isAdhoc = false
    let backupTool = null
    let epoch = null
    let serverSegmentIndex = segments.length - 1

    if (mysqldumpPattern.test(lastSegment)) {
      isAdhoc = true
      backupTool = 'mysqldump'
      epoch = lastSegment.match(mysqldumpPattern)[1]
      serverSegmentIndex = segments.length - 2
    } else if (dumplingPattern.test(lastSegment)) {
      isAdhoc = true
      backupTool = 'dumpling'
      epoch = lastSegment.match(dumplingPattern)[1]
      serverSegmentIndex = segments.length - 2
    } else if (mydumperPattern.test(lastSegment)) {
      isAdhoc = true
      backupTool = 'mydumper'
      epoch = lastSegment.match(mydumperPattern)[1]
      serverSegmentIndex = segments.length - 2
    } else if (isAdhocTagged) {
      isAdhoc = true
    }

    const serverSegment = segments[serverSegmentIndex]
    const clusterName = segments[serverSegmentIndex - 1] || 'N/A'

    const serverParts = serverSegment.split('_')
    if (serverParts.length >= 2) {
      const serverPort = serverParts[serverParts.length - 1]
      const serverHost = serverParts.slice(0, -1).join('_')

      return {
        clusterName,
        serverHost,
        serverPort,
        isAdhoc,
        backupTool,
        epoch
      }
    }

    return {
      clusterName,
      serverHost: serverSegment,
      serverPort: 'N/A',
      isAdhoc,
      backupTool,
      epoch
    }
  } catch (error) {
    return {
      clusterName: 'N/A',
      serverHost: 'N/A',
      serverPort: 'N/A',
      isAdhoc: false,
      backupTool: null,
      epoch: null
    }
  }
}

const formatEpochDateTime = (epoch) => {
  try {
    const date = new Date(parseInt(epoch) * 1000)
    return date.toLocaleString('en-US', {
      year: 'numeric',
      month: 'short',
      day: '2-digit',
      hour: '2-digit',
      minute: '2-digit',
      second: '2-digit',
      hour12: false
    })
  } catch (error) {
    return epoch
  }
}

const formatLocalDateTime = (timestamp) => {
  try {
    const date = new Date(timestamp)
    return date.toLocaleString('en-US', {
      year: 'numeric',
      month: 'short',
      day: '2-digit',
      hour: '2-digit',
      minute: '2-digit',
      second: '2-digit',
      hour12: false
    })
  } catch (error) {
    return timestamp
  }
}

export {
  extractServerInfoFromPath,
  formatEpochDateTime,
  formatLocalDateTime,
  formatMetadataTimestamp,
  getPreferredMetadata,
  getSnapshotMetadataByMethod,
  parseSnapshotTags
}
