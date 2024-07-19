import {
  AccordionPanel,
  Box,
  Flex,
  IconButton,
  SimpleGrid,
  Spacer,
  Tooltip,
  useColorMode,
  VStack
} from '@chakra-ui/react'
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
import AccordionComponent from '../../../../components/AccordionComponent'

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

  const tagPillSize = 'sm'

  const styles = {
    card: {
      borderRadius: '16px',
      border: '1px solid',
      width: '100%',
      gap: '0',
      borderColor: colorMode === 'light' ? `blue.200` : `blue.900`
    },

    header: {
      textAlign: 'center',
      p: '4px',
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
      padding: '0.5',
      marginTop: '2',
      fontSize: '14px'
    },
    accordionHeader: {
      borderRadius: '0',
      padding: '6px',
      fontSize: '14px'
    },
    accordionPanel: {
      borderRadius: '0',
      border: 'none'
    }
  }

  return (
    <SimpleGrid columns={{ base: 1, sm: 1, md: 2, lg: 3 }} spacing={2} spacingY={6} spacingX={6} marginTop='4px'>
      {allDBServers?.length > 0 &&
        allDBServers.map((rowData) => {
          const serverInfoData = [
            {
              key: 'Version',
              value: getVersion(rowData)
            },
            {
              key: 'Internal Id',
              value: rowData.id
            },
            {
              key: 'Fail Count',
              value: getFailCount(rowData)
            }
          ]
          const replicationVariables = [
            {
              key: 'DB Server Id',
              value: rowData.serverId
            },
            {
              key: 'Queue size',
              value: rowData?.slaveVariables?.slaveParallelMaxQueued
            },
            {
              key: 'Threads',
              value: rowData?.slaveVariables?.slaveParallelThreads
            },
            {
              key: 'Workers',
              value: rowData?.slaveVariables?.slaveParallelWorkers
            }
          ]

          const leaderStatus = [
            {
              key: 'Current log',
              value: rowData.binaryLogFile
            },
            {
              key: 'Oldest log',
              value: rowData.binaryLogFileOldest
            },
            {
              key: 'First log at',
              value: rowData.binaryLogOldestTimestamp
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

              <Flex direction='column' width='100%' mb={2} gap='0'>
                <Flex gap='1' wrap='wrap' p='2'>
                  <TagPill
                    size={tagPillSize}
                    colorScheme={getStatusValue(rowData).split('|')[0]}
                    text={getStatusValue(rowData).split('|')[1]}
                  />
                  {rowData.isMaintenance && <TagPill colorScheme='red' text={'IN_MAINTENANCE'} />}

                  {(rowData.prefered || rowData.ignored) && (
                    <TagPill
                      size={tagPillSize}
                      colorScheme={rowData.prefered ? 'green' : 'red'}
                      text={rowData.prefered ? 'PREFERRED' : 'IGNORED'}
                    />
                  )}
                  {rowData.readOnly === 'ON' && (
                    <TagPill size={tagPillSize} colorScheme={rowData.isSlave ? 'green' : 'red'} text={'READ_ONLY'} />
                  )}

                  {rowData.ignoredRO && rowData.isSlave && (
                    <TagPill size={tagPillSize} colorScheme='green' text={'FORCE_WRITE'} />
                  )}

                  {rowData.eventScheduler && (
                    <TagPill size={tagPillSize} colorScheme={rowData.isSlave ? 'red' : 'green'} text={'SCHEDULER'} />
                  )}

                  <TagPill size={tagPillSize} text={rowData?.slaveVariables?.slaveParallelMode} />
                  <TagPill size={tagPillSize} text={rowData?.slaveVariables?.slaveTypeConversions} />
                </Flex>
                <AccordionComponent
                  heading={'Server Information'}
                  headerSX={styles.accordionHeader}
                  panelSX={styles.accordionPanel}
                  body={
                    <TableType2
                      dataArray={serverInfoData}
                      templateColumns='30% auto'
                      gap={1}
                      boxPadding={1}
                      minHeight='24px'
                      sx={styles.tableType2}
                    />
                  }
                />

                <AccordionComponent
                  heading={'Replication Variables'}
                  headerSX={styles.accordionHeader}
                  panelSX={styles.accordionPanel}
                  body={
                    <TableType2
                      dataArray={replicationVariables}
                      templateColumns='30% auto'
                      gap={1}
                      boxPadding={1}
                      minHeight='24px'
                      sx={styles.tableType2}
                    />
                  }
                />
                <AccordionComponent
                  heading={'Leader status'}
                  headerSX={styles.accordionHeader}
                  panelSX={styles.accordionPanel}
                  body={
                    <TableType2
                      dataArray={leaderStatus}
                      boxPadding={1}
                      templateColumns='30% auto'
                      gap={1}
                      minHeight='24px'
                      sx={styles.tableType2}
                    />
                  }
                />
                {rowData.replications?.length > 0 &&
                  rowData.replications.map((replication, index) => {
                    const replicationTableData = [
                      {
                        key: 'Semi Sync',
                        value:
                          (rowData.state === 'Slave' && rowData.semiSyncSlaveStatus) ||
                          (rowData.state === 'Master' && rowData.semiSyncMasterStatus) ? (
                            <CustomIcon icon={HiCheck} color='green' />
                          ) : (
                            <CustomIcon icon={HiX} color='red' />
                          )
                      },
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
                      }
                    ]

                    const replicationTableData2 = [
                      {
                        key: 'Master',
                        value: `${replication?.masterHost?.String} ${replication?.masterPort?.String}`
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
                        key: 'Delay',
                        value: getDelay(rowData)
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
                      <AccordionComponent
                        headerSX={styles.accordionHeader}
                        panelSX={styles.accordionPanel}
                        heading={
                          replication.connectionName.String
                            ? `Replication Status (${replication.connectionName.String})`
                            : 'Unnamed Replication Status'
                        }
                        body={
                          <Flex key={index} direction='column' mt={4}>
                            <TableType3 dataArray={replicationTableData} />
                            <TableType2
                              dataArray={replicationTableData2}
                              sx={styles.tableType2}
                              gap={1}
                              boxPadding={1}
                              minHeight='24px'
                              templateColumns='30% auto'
                            />
                          </Flex>
                        }
                      />
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
