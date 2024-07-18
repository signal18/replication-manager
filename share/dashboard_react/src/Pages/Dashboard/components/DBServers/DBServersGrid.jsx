import { Box, Flex, IconButton, SimpleGrid, Spacer, Tooltip, useColorMode, VStack } from '@chakra-ui/react'
import React from 'react'
import { useSelector } from 'react-redux'
import ServerMenu from './ServerMenu'
import { HiCheck, HiTable, HiX } from 'react-icons/hi'
import { MdCompare } from 'react-icons/md'
import TableType3 from '../../../../components/TableType3'
import CustomIcon from '../../../../components/Icons/CustomIcon'
import TableType2 from '../../../../components/TableType2'
import {
  getCurrentGtid,
  getCurrentGtidHeader,
  getDelay,
  getFailCount,
  getSlaveGtid,
  getSlaveGtidHeader,
  getStatusValue,
  getUsingGtid,
  getUsingGtidHeader,
  getVersion
} from './utils'
import TagPill from '../../../../components/TagPill'
import CheckOrCrossIcon from '../../../../components/Icons/CheckOrCrossIcon'
import DBFlavourIcon from '../../../../components/Icons/DBFlavourIcon'
import ServerName from './ServerName'

function DBServersGrid({
  allDBServers,
  clusterMasterId,
  clusterName,
  user,
  showTableView,
  openCompareModal,
  hasMariadbGtid,
  hasMysqlGtid
}) {
  const {
    common: { isDesktop, isTablet, isMobile }
  } = useSelector((state) => state)
  const { colorMode } = useColorMode()

  const styles = {
    card: {
      borderRadius: '16px',
      border: '1px solid',
      width: '100%',
      borderColor: colorMode === 'light' ? `blue.200` : `blue.900`
    },

    header: {
      textAlign: 'center',
      p: '16px',
      bg: colorMode === 'light' ? `blue.200` : `blue.900`,
      borderTopLeftRadius: '16px',
      borderTopRightRadius: '16px',
      color: '#000',
      fontWeight: 'bold'
    },
    replicationTitle: {
      textAlign: 'center',
      fontWeight: 'bold',
      marginBottom: '2',
      backgroundColor: colorMode === 'light' ? `blue.100` : `blue.800`,
      py: '2',
      width: '50%',
      margin: 'auto',
      borderRadius: '16px',
      marginTop: '8px'
    },
    tableType2: {
      padding: '1',
      marginTop: '2'
    }
  }

  return (
    <SimpleGrid columns={{ base: 1, sm: 1, md: 2, lg: 3 }} spacing={2} spacingY={6} spacingX={6} marginTop='24px'>
      {allDBServers?.length > 0 &&
        allDBServers.map((rowData) => {
          const tableData1 = [
            {
              key: 'Status',
              value: (
                <TagPill
                  colorScheme={getStatusValue(rowData).split('|')[0]}
                  text={getStatusValue(rowData).split('|')[1]}
                />
              )
            },
            { key: 'In Maintenance', value: <CheckOrCrossIcon isValid={rowData.isMaintenance} /> },
            {
              key: 'Prefered / Ignored',
              value: <CheckOrCrossIcon isValid={rowData.prefered} isInvalid={rowData.ignored} variant='thumb' />
            },
            { key: 'Read Only', value: <CheckOrCrossIcon isValid={rowData.readOnly == 'ON'} /> },
            { key: 'Ignore Read Only', value: <CheckOrCrossIcon isValid={rowData.ignoredRO} /> },
            { key: 'Event Scheduler', value: <CheckOrCrossIcon isValid={rowData.eventScheduler} /> }
          ]
          const tableData2 = [
            {
              key: 'Version',
              value: getVersion(rowData)
            },
            {
              key: 'Internal Id',
              value: rowData.id
            },
            {
              key: 'DB Server Id',
              value: rowData.serverId
            },
            {
              key: 'Fail Count',
              value: getFailCount(rowData)
            },
            {
              key: getUsingGtidHeader(hasMariadbGtid, hasMysqlGtid),
              value: getUsingGtid(rowData, hasMariadbGtid, hasMysqlGtid)
            },
            {
              key: getCurrentGtidHeader(hasMariadbGtid, hasMysqlGtid),
              value: getCurrentGtid(rowData, hasMariadbGtid, hasMysqlGtid)
            },
            {
              key: getSlaveGtidHeader(hasMariadbGtid, hasMysqlGtid),
              value: getSlaveGtid(rowData, hasMariadbGtid, hasMysqlGtid)
            },
            {
              key: 'Binary Log',
              value: rowData.binaryLogFile
            },
            {
              key: 'Binary Log Oldest',
              value: rowData.binaryLogFileOldest
            },
            {
              key: 'Binary Log Oldest Timestamp	',
              value: rowData.binaryLogOldestTimestamp //rowData.binaryLogOldestTimestamp>0 &&
            },
            {
              key: 'Slave parallel max queued',
              value: rowData?.slaveVariables?.slaveParallelMaxQueued
            },
            {
              key: 'Slave parallel mode',
              value: rowData?.slaveVariables?.slaveParallelMode
            },
            {
              key: 'Slave parallel threads',
              value: rowData?.slaveVariables?.slaveParallelThreads
            },
            {
              key: 'Slave parallel workers',
              value: rowData?.slaveVariables?.slaveParallelWorkers
            },
            {
              key: 'Slave type conversions',
              value: rowData?.slaveVariables?.slaveTypeConversions
            }
          ]

          return (
            <VStack width='100%' key={rowData.id} sx={styles.card}>
              <Flex as='header' width='100%' sx={styles.header} align='center'>
                <DBFlavourIcon dbFlavor={rowData.dbVersion.flavor} />
                <ServerName rowData={rowData} />
                <Spacer />
                <Tooltip label='Compare servers'>
                  <IconButton
                    icon={<MdCompare />}
                    onClick={() => openCompareModal(rowData)}
                    size='sm'
                    fontSize='1.5rem'
                    marginRight={2}
                  />
                </Tooltip>
                <Tooltip label='Show table view'>
                  <IconButton icon={<HiTable />} onClick={showTableView} size='sm' fontSize='1.5rem' marginRight={2} />
                </Tooltip>
                <ServerMenu
                  from='gridView'
                  clusterMasterId={clusterMasterId}
                  row={rowData}
                  clusterName={clusterName}
                  isDesktop={isDesktop}
                  user={user}
                  openCompareModal={openCompareModal}
                />
              </Flex>
              <Flex direction='column' width='100%' mb={2}>
                <TableType3 dataArray={tableData1} />
                <TableType2 dataArray={tableData2} templateColumns='30% auto' gap={1} sx={styles.tableType2} />
                <Box sx={styles.replicationTitle}>
                  {rowData.replications?.length > 0
                    ? `Replications (${rowData.replications.length})`
                    : 'No replications found'}
                </Box>
                {rowData.replications?.length > 0 &&
                  rowData.replications.map((replication, index) => {
                    const replicationTableData = [
                      {
                        key: 'IO Thread',
                        value:
                          replication.slaveIoRunning?.String == 'Yes' ? (
                            <CustomIcon icon={HiCheck} color='green' />
                          ) : (
                            <CustomIcon icon={HiX} color='red' />
                          )
                      },
                      {
                        key: 'SQL Thread',
                        value:
                          replication.slaveSqlRunning?.String == 'Yes' ? (
                            <CustomIcon icon={HiCheck} color='green' />
                          ) : (
                            <CustomIcon icon={HiX} color='red' />
                          )
                      },
                      {
                        key: 'Master Sync',
                        value: rowData.semiSyncMasterStatus ? (
                          <CustomIcon icon={HiCheck} color='green' />
                        ) : (
                          <CustomIcon icon={HiX} color='red' />
                        )
                      },
                      {
                        key: 'Slave Sync',
                        value: rowData.semiSyncSlaveStatus ? (
                          <CustomIcon icon={HiCheck} color='green' />
                        ) : (
                          <CustomIcon icon={HiX} color='red' />
                        )
                      }
                    ]

                    const replicationTableData2 = [
                      {
                        key: 'Source',
                        value: replication.connectionName.String
                      },
                      {
                        key: 'Delay',
                        value: getDelay(rowData)
                      },
                      {
                        key: 'Master',
                        value: `${replication?.masterHost?.String} ${replication?.masterPort?.String}`
                      },
                      {
                        key: 'SQL error',
                        value: replication?.lastSqlError?.String
                      },
                      {
                        key: 'IO error',
                        value: replication?.lastIoError?.String
                      }
                    ]
                    return (
                      <Flex key={index} direction='column' mt={4}>
                        <TableType3 dataArray={replicationTableData} />
                        <TableType2
                          dataArray={replicationTableData2}
                          sx={styles.tableType2}
                          gap={1}
                          templateColumns='30% auto'
                        />
                      </Flex>
                    )
                  })}
              </Flex>
            </VStack>
          )
        })}
    </SimpleGrid>
  )
}

export default DBServersGrid
