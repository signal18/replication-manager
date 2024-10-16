import { gtidstring } from '../../../../utility/common'

export const getUsingGtid = (rowData, hasMariadbGtid, hasMysqlGtid) => {
  if (hasMariadbGtid) {
    return rowData.replications?.length > 0 && rowData.replications[0].usingGtid.String
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
    if (rowData.isSlave && rowData.replications?.length > 0) {
      result += rowData.replications[0].masterLogFile.String
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
    if (rowData.isSlave && rowData.replications?.length > 0) {
      result += rowData.replications[0].execMasterLogPos.String
    } else {
      result += rowData.binaryLogPos
    }
  }
  const array = result.split(', ')
  return array.join(',\r\n')
}

export const getUsingGtidHeader = (hasMariadbGtid, hasMysqlGtid) => {
  return `${hasMariadbGtid && 'Using GTID'} ${hasMariadbGtid && hasMysqlGtid ? '/' : ''} ${hasMysqlGtid ? 'Executed GTID Set' : ''}`
}

export const getCurrentGtidHeader = (hasMariadbGtid, hasMysqlGtid) => {
  return hasMariadbGtid ? 'Current GTID' : !hasMariadbGtid && !hasMysqlGtid ? 'File' : ''
}

export const getSlaveGtidHeader = (hasMariadbGtid, hasMysqlGtid) => {
  return hasMariadbGtid ? 'Slave GTID' : !hasMariadbGtid && !hasMysqlGtid ? 'Pos' : ''
}

export const getDelay = (rowData) => {
  return rowData.replications?.length > 0 && rowData.replications[0].secondsBehindMaster.Int64
}

export const getFailCount = (rowData) => {
  return `${rowData.failCount}/${rowData.failSuspectHeartbeat}`
}

export const getVersion = (rowData) => {
  return `${rowData.dbVersion.flavor} ${rowData.dbVersion.major} ${rowData.dbVersion.minor} ${rowData.dbVersion.release}`
}
