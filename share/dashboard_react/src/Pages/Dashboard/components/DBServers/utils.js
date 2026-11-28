import { gtidstring } from '../../../../utility/common'

// a server's replications are one list: its native channels, then the CDC feeds writing into it
// (kind 'cdc'); the channel columns read the channels, the feed block the feeds
export const channelsOf = (rowData) => (rowData?.replications || []).filter((r) => r.kind !== 'cdc')
export const feedsOf = (rowData) => (rowData?.replications || []).filter((r) => r.kind === 'cdc')

export const getUsingGtid = (rowData, hasMariadbGtid, hasMysqlGtid) => {
  if (hasMariadbGtid) {
    return channelsOf(rowData).length > 0 && channelsOf(rowData)[0].usingGtid.String
  } else if (hasMysqlGtid) {
    return rowData.gtidExecuted
  }
}

export const getCurrentGtid = (rowData, hasMariadbGtid, hasMysqlGtid) => {
  let result = ''
  if (hasMariadbGtid) {
    result = gtidstring(rowData.currentGtid)
  }

  if (!hasMariadbGtid && !hasMysqlGtid) {
    if (rowData.isSlave && channelsOf(rowData).length > 0) {
      result += channelsOf(rowData)[0].masterLogFile.String
    } else {
      result += rowData.binaryLogFile
    }
  }

  const array = result.split(', ')
  return array.join(',\r\n')
}

export const getSlaveGtid = (rowData, hasMariadbGtid, hasMysqlGtid) => {
  let result = ''
  if (hasMariadbGtid) {
    result = gtidstring(rowData.slaveGtid)
  }
  if (!hasMariadbGtid && !hasMysqlGtid) {
    if (rowData.isSlave && channelsOf(rowData).length > 0) {
      result += channelsOf(rowData)[0].execMasterLogPos.String
    } else {
      result += rowData.binaryLogPos
    }
  }
  const array = result.split(', ')
  return array.join(',\r\n')
}

export const getUsingGtidHeader = (hasMariadbGtid, hasMysqlGtid) => {
  return `${hasMariadbGtid ? 'Using GTID' : ''} ${hasMariadbGtid && hasMysqlGtid ? '/' : ''} ${hasMysqlGtid ? 'Executed GTID Set' : ''}`
}

export const getCurrentGtidHeader = (hasMariadbGtid, hasMysqlGtid) => {
  return hasMariadbGtid ? 'Current GTID' : !hasMariadbGtid && !hasMysqlGtid ? 'File' : ''
}

export const getSlaveGtidHeader = (hasMariadbGtid, hasMysqlGtid) => {
  return hasMariadbGtid ? 'Slave GTID' : !hasMariadbGtid && !hasMysqlGtid ? 'Pos' : ''
}

export const getDelay = (rowData) => {
  return channelsOf(rowData).length > 0 && channelsOf(rowData)[0].secondsBehindMaster.Int64
}

export const getFailCount = (rowData) => {
  return `${rowData.failCount}/${rowData.failSuspectHeartbeat}`
}

export const getVersion = (rowData) => {
  return `${rowData.dbVersion.flavor} ${rowData.dbVersion.major} ${rowData.dbVersion.minor} ${rowData.dbVersion.release}`
}

// host:port of a server, with the port repman monitors it on when that is another
// one: db-servers-monitor-extra-port, MariaDB extra_port (#2150)
export const getServerDisplayName = (rowData) =>
  rowData?.monitorPort && rowData.monitorPort !== rowData.port
    ? `${rowData.host}:${rowData.port} (monitored on :${rowData.monitorPort})`
    : `${rowData?.host}:${rowData?.port}`
