import { Flex, SimpleGrid, Spacer, Tooltip, useColorMode, useDisclosure, VStack } from '@chakra-ui/react'
import React from 'react'
import { useSelector } from 'react-redux'
import ServerMenu from '../ServerMenu'
import { HiCheck, HiTable, HiX } from 'react-icons/hi'
import { MdCompare } from 'react-icons/md'
import TableType3 from '../../../../../components/TableType3'
import CustomIcon from '../../../../../components/Icons/CustomIcon'
import TableType2 from '../../../../../components/TableType2'
import {
  getCurrentGtid,
  getCurrentGtidHeader,
  getDelay,
  getFailCount,
  getSlaveGtid,
  getSlaveGtidHeader,
  getUsingGtid,
  getUsingGtidHeader,
  getVersion
} from '../utils'
import TagPill from '../../../../../components/TagPill'
import DBFlavourIcon from '../../../../../components/Icons/DBFlavourIcon'
import ServerName from '../ServerName'
import AccordionComponent from '../../../../../components/AccordionComponent'
import NotFound from '../../../../../components/NotFound'
import GTID from '../../../../../components/GTID'
import ServerStatus from '../../../../../components/ServerStatus'
import RMIconButton from '../../../../../components/RMIconButton'
import cssStyles from './styles.module.scss'

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
    common: { isDesktop }
  } = useSelector((state) => state)
  const { colorMode } = useColorMode()

  const { isOpen: isServiceInfoOpen, onToggle: onServiceInfoToggle } = useDisclosure({ defaultIsOpen: false })
  const { isOpen: isReplicationVarOpen, onToggle: onReplicationVarToggle } = useDisclosure({ defaultIsOpen: false })
  const { isOpen: isLeaderStatusOpen, onToggle: onLeaderStatusToggle } = useDisclosure({ defaultIsOpen: false })
  const { isOpen: isReplicationStatusOpen, onToggle: onReplicationStatusToggle } = useDisclosure({
    defaultIsOpen: true
  })

  const styles = {
    card: {
      borderRadius: '16px',
      border: '1px solid',
      width: '100%',
      gap: '0',
      borderColor: colorMode === 'light' ? `blue.200` : `blue.900`
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
    }
  }

  return (
    <SimpleGrid columns={{ base: 1, sm: 1, md: 2, lg: 3 }} spacing={2} spacingY={6} spacingX={6} marginTop='4px'>
      {allDBServers?.length > 0 &&
        allDBServers.map((rowData) => {
          const replicationTags = rowData.replicationTags?.length > 0 ? rowData.replicationTags.split(' ') : []
          let gridColor = ''
          switch (rowData.state) {
            case 'SlaveErr':
              gridColor = 'orange'
              break
            case 'SlaveLate':
              gridColor = 'orange'
              break
            case 'Suspect':
              gridColor = 'orange'
              break
            case 'Failed':
              gridColor = 'red'
              break
          }
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
            <VStack width='100%' key={rowData.id} sx={{ ...styles.card }}>
              <Flex as='header' width='100%' className={`${cssStyles.header} ${cssStyles[gridColor]}`} align='center'>
                <DBFlavourIcon dbFlavor={rowData.dbVersion.flavor} isBlocking={gridColor.length > 0} from='gridView' />
                <ServerName as='h4' rowData={rowData} isBlocking={gridColor.length > 0} />
                <Spacer />

                <RMIconButton
                  icon={MdCompare}
                  onClick={() => openCompareModal(rowData)}
                  marginRight={2}
                  tooltip='Compare servers'
                  {...(gridColor.length > 0 ? { colorScheme: gridColor } : {})}
                />

                <RMIconButton
                  icon={HiTable}
                  onClick={showTableView}
                  marginRight={2}
                  tooltip='Show table view'
                  {...(gridColor.length > 0 ? { colorScheme: gridColor } : {})}
                />

                <ServerMenu
                  from='gridView'
                  clusterMasterId={clusterMasterId}
                  row={rowData}
                  clusterName={clusterName}
                  isDesktop={isDesktop}
                  user={user}
                  openCompareModal={openCompareModal}
                  {...(gridColor.length > 0 ? { colorScheme: gridColor } : {})}
                />
              </Flex>

              <Flex
                direction='column'
                width='100%'
                mb={2}
                gap='0'
                className={`${cssStyles.body} ${cssStyles[gridColor]}`}>
                <Flex gap='1' wrap='wrap' p='2'>
                  <ServerStatus state={rowData.state} isVirtualMaster={rowData.isVirtualMaster} isBlinking={true} />
                  {replicationTags
                    .filter((tag) => tag === 'READ_ONLY' || tag === 'NO_READ_ONLY')
                    .map((tag, index) => (
                      <TagPill key={index} colorScheme={tag.startsWith('NO_') ? 'red' : 'green'} text={tag} />
                    ))}
                </Flex>
                {rowData.replications?.length > 0 ? (
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
                        key: 'Semi Sync',
                        value:
                          (rowData.state === 'Slave' && rowData.semiSyncSlaveStatus) ||
                          (rowData.state === 'Master' && rowData.semiSyncMasterStatus) ? (
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
                        value: (
                          <GTID text={getCurrentGtid(rowData, hasMariadbGtid, hasMysqlGtid)} copyIconPosition='end' />
                        )
                      },
                      {
                        key: getSlaveGtidHeader(hasMariadbGtid, hasMysqlGtid),
                        value: (
                          <GTID text={getSlaveGtid(rowData, hasMariadbGtid, hasMysqlGtid)} copyIconPosition='end' />
                        )
                      },
                      {
                        key: 'Delay',
                        value: getDelay(rowData)
                      },
                      {
                        key: getSlaveGtidHeader(hasMariadbGtid, hasMysqlGtid),
                        value: (
                          <GTID text={getSlaveGtid(rowData, hasMariadbGtid, hasMysqlGtid)} copyIconPosition='end' />
                        )
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
                        key={index}
                        headerClassName={`${cssStyles.accordionHeader} ${cssStyles[gridColor]}`}
                        panelClassName={cssStyles.accordionPanel}
                        isOpen={isReplicationStatusOpen}
                        onToggle={onReplicationStatusToggle}
                        heading={
                          replication.connectionName.String
                            ? `Replication Status (${replication.connectionName.String})`
                            : 'Unnamed Replication Status'
                        }
                        body={
                          <Flex key={index} direction='column' mt={1}>
                            <TableType3
                              dataArray={replicationTableData}
                              isBlocking={gridColor.length > 0}
                              color={gridColor}
                            />
                            <TableType2 dataArray={replicationTableData2} templateColumns='30% auto' />
                          </Flex>
                        }
                      />
                    )
                  })
                ) : (
                  <AccordionComponent
                    headerClassName={`${cssStyles.accordionHeader} ${cssStyles[gridColor]}`}
                    panelClassName={cssStyles.accordionPanel}
                    isOpen={isReplicationStatusOpen}
                    onToggle={onReplicationStatusToggle}
                    heading={'No Replication Status'}
                    body={
                      <Flex direction='column' mt={4} minHeight='295px' align='center' justify='flex-start'>
                        <NotFound text={'No replication status data found'} />
                        <TableType2
                          dataArray={[
                            {
                              key: getUsingGtidHeader(hasMariadbGtid, hasMysqlGtid),
                              value: getUsingGtid(rowData, hasMariadbGtid, hasMysqlGtid)
                            },
                            {
                              key: getCurrentGtidHeader(hasMariadbGtid, hasMysqlGtid),
                              value: (
                                <GTID
                                  text={getCurrentGtid(rowData, hasMariadbGtid, hasMysqlGtid)}
                                  copyIconPosition='end'
                                />
                              )
                            },
                            {
                              key: getSlaveGtidHeader(hasMariadbGtid, hasMysqlGtid),
                              value: (
                                <GTID
                                  text={getSlaveGtid(rowData, hasMariadbGtid, hasMysqlGtid)}
                                  copyIconPosition='end'
                                />
                              )
                            }
                          ]}
                          templateColumns='30% auto'
                        />
                      </Flex>
                    }
                  />
                )}
                <AccordionComponent
                  heading={'Server Information'}
                  headerClassName={`${cssStyles.accordionHeader} ${cssStyles[gridColor]}`}
                  panelClassName={cssStyles.accordionPanel}
                  isOpen={isServiceInfoOpen}
                  onToggle={onServiceInfoToggle}
                  body={<TableType2 dataArray={serverInfoData} templateColumns='30% auto' />}
                />

                <AccordionComponent
                  heading={'Replication Variables'}
                  headerClassName={`${cssStyles.accordionHeader} ${cssStyles[gridColor]}`}
                  panelClassName={cssStyles.accordionPanel}
                  isOpen={isReplicationVarOpen}
                  onToggle={onReplicationVarToggle}
                  body={
                    <Flex direction='column'>
                      <Flex gap='1' wrap='wrap' p='2'>
                        {replicationTags
                          .filter((tag) => tag.length > 0)
                          .map((tag, index) => (
                            <TagPill key={index} colorScheme={tag.startsWith('NO_') ? 'red' : 'green'} text={tag} />
                          ))}
                      </Flex>
                      <TableType2 dataArray={replicationVariables} templateColumns='30% auto' />
                    </Flex>
                  }
                />
                <AccordionComponent
                  heading={'Leader status'}
                  headerClassName={`${cssStyles.accordionHeader} ${cssStyles[gridColor]}`}
                  panelClassName={cssStyles.accordionPanel}
                  isOpen={isLeaderStatusOpen}
                  onToggle={onLeaderStatusToggle}
                  body={<TableType2 dataArray={leaderStatus} templateColumns='30% auto' />}
                />
              </Flex>
            </VStack>
          )
        })}
    </SimpleGrid>
  )
}

export default DBServersGrid
