import {
  Box,
  Button,
  Flex,
  Modal,
  ModalBody,
  ModalCloseButton,
  ModalContent,
  ModalHeader,
  ModalOverlay,
  Text
} from '@chakra-ui/react'
import React, { useState, useEffect } from 'react'
import Dropdown from '../Dropdown'
import TableType4Compare from '../TableType4Compare'
import { useSelector } from 'react-redux'
import TagPill from '../TagPill'
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
} from '../../Pages/Dashboard/components/DBServers/utils'
import CheckOrCrossIcon from '../Icons/CheckOrCrossIcon'
import DBFlavourIcon from '../Icons/DBFlavourIcon'

function CompareModal({ isOpen, closeModal, allDBServers, compareServer, hasMariadbGtid, hasMysqlGtid }) {
  const [selectedServer, setSelectedServer] = useState(null)
  const [serverOptions, setServerOptions] = useState([])
  console.log('compare server::', compareServer, selectedServer)
  const {
    common: { isDesktop, isTablet, isMobile }
  } = useSelector((state) => state)

  useEffect(() => {
    let servers = []
    if (allDBServers?.length > 0) {
      const filtered = allDBServers.filter((server) => server.id !== compareServer.id)
      servers = filtered.map((server) => {
        return { name: `${server.host}:${server.port}`, value: server.id, data: server }
      })
      setServerOptions(servers)
    }
  }, [allDBServers])

  return (
    <Modal isOpen={isOpen} onClose={closeModal}>
      <ModalOverlay />
      <ModalContent
        width={isDesktop ? '60%' : isTablet ? '90%' : '97%'}
        maxWidth='none'
        minHeight='300px'
        textAlign='center'>
        <ModalHeader whiteSpace='pre-line'>
          {selectedServer
            ? ''
            : `Please select a database to be compared with \n  ${compareServer.host}:${compareServer.port}`}
        </ModalHeader>
        <ModalCloseButton />
        <ModalBody>
          {selectedServer ? (
            <Flex direction={'column'} gap={4} maxHeight='80vh' overflow='auto'>
              <Button width='fit-content' p='3' onClick={() => setSelectedServer(null)}>
                Change
              </Button>
              <TableType4Compare
                item1Title={`${compareServer.host}:${compareServer.port}`}
                item2Title={`${selectedServer.host}:${selectedServer.port}`}
                dataArray={[
                  {
                    key: 'DB Flavor',
                    value1: (
                      <Box display='flex' alignItems='center' gap='8px'>
                        <DBFlavourIcon dbFlavor={compareServer.dbVersion.flavor} />
                        <Text>{compareServer.dbVersion.flavor}</Text>
                      </Box>
                    ),
                    value2: (
                      <Box display='flex' alignItems='center' gap='8px'>
                        <DBFlavourIcon dbFlavor={selectedServer.dbVersion.flavor} />
                        <Text>{selectedServer.dbVersion.flavor}</Text>
                      </Box>
                    )
                  },
                  {
                    key: 'Status',
                    value1: (
                      <TagPill
                        colorScheme={getStatusValue(compareServer).split('|')[0]}
                        text={getStatusValue(compareServer).split('|')[1]}
                      />
                    ),
                    value2: (
                      <TagPill
                        colorScheme={getStatusValue(selectedServer).split('|')[0]}
                        text={getStatusValue(selectedServer).split('|')[1]}
                      />
                    )
                  },
                  {
                    key: 'In Maintenance',
                    value1: <CheckOrCrossIcon isValid={compareServer.isMaintenance} />,
                    value2: <CheckOrCrossIcon isValid={selectedServer.isMaintenance} />
                  },
                  {
                    key: 'Ignored/Preferred',
                    value1: (
                      <CheckOrCrossIcon
                        isValid={compareServer.prefered}
                        isInvalid={compareServer.ignored}
                        variant='thumb'
                      />
                    ),
                    value2: (
                      <CheckOrCrossIcon
                        isValid={selectedServer.prefered}
                        isInvalid={selectedServer.ignored}
                        variant='thumb'
                      />
                    )
                  },
                  {
                    key: 'Read Only',
                    value1: <CheckOrCrossIcon isValid={compareServer.readOnly == 'ON'} />,
                    value2: <CheckOrCrossIcon isValid={selectedServer.readOnly == 'ON'} />
                  },
                  {
                    key: 'Ignore Read Only',
                    value1: <CheckOrCrossIcon isValid={compareServer.ignoredRO} />,
                    value2: <CheckOrCrossIcon isValid={selectedServer.ignoredRO} />
                  },
                  {
                    key: 'Event Scheduler',
                    value1: <CheckOrCrossIcon isValid={compareServer.eventScheduler} />,
                    value2: <CheckOrCrossIcon isValid={selectedServer.eventScheduler} />
                  },
                  { key: 'Version', value1: getVersion(compareServer), value2: getVersion(selectedServer) },
                  { key: 'Internal Id', value1: compareServer.id, value2: selectedServer.id },
                  { key: 'DB Server Id', value1: compareServer.serverId, value2: selectedServer.serverId },
                  { key: 'Fail Count', value1: getFailCount(compareServer), value2: getFailCount(selectedServer) },
                  { key: 'Binary Log', value1: compareServer.binaryLogFile, value2: selectedServer.binaryLogFile },
                  {
                    key: 'Binary Log Oldest',
                    value1: compareServer.binaryLogFileOldest,
                    value2: selectedServer.binaryLogFileOldest
                  },
                  {
                    key: 'Binary Log Oldest Timestamp',
                    value1: compareServer.binaryLogOldestTimestamp,
                    value2: selectedServer.binaryLogOldestTimestamp
                  },
                  {
                    key: getUsingGtidHeader(hasMariadbGtid, hasMysqlGtid),
                    value1: getUsingGtid(compareServer, hasMariadbGtid, hasMysqlGtid),
                    value2: getUsingGtid(selectedServer, hasMariadbGtid, hasMysqlGtid)
                  },
                  {
                    key: getCurrentGtidHeader(hasMariadbGtid, hasMysqlGtid),
                    value1: getCurrentGtid(compareServer, hasMariadbGtid, hasMysqlGtid),
                    value2: getCurrentGtid(selectedServer, hasMariadbGtid, hasMysqlGtid)
                  },
                  {
                    key: getSlaveGtidHeader(hasMariadbGtid, hasMysqlGtid),
                    value1: getSlaveGtid(compareServer, hasMariadbGtid, hasMysqlGtid),
                    value2: getSlaveGtid(selectedServer, hasMariadbGtid, hasMysqlGtid)
                  },
                  { key: 'Delay', value1: getDelay(compareServer), value2: getDelay(selectedServer) },
                  {
                    key: 'Slave parallel max queued',
                    value1: compareServer.slaveVariables?.slaveParallelMaxQueued,
                    value2: selectedServer.slaveVariables?.slaveParallelMaxQueued
                  },
                  {
                    key: 'Slave parallel mode',
                    value1: compareServer.slaveVariables?.slaveParallelMode,
                    value2: selectedServer.slaveVariables?.slaveParallelMode
                  },
                  {
                    key: 'Slave parallel threads',
                    value1: compareServer.slaveVariables?.slaveParallelThreads,
                    value2: selectedServer.slaveVariables?.slaveParallelThreads
                  },
                  {
                    key: 'Slave parallel workers',
                    value1: compareServer.slaveVariables?.slaveParallelWorkers,
                    value2: selectedServer.slaveVariables?.slaveParallelWorkers
                  },
                  {
                    key: 'Slave type conversions',
                    value1: compareServer.slaveVariables?.slaveTypeConversions,
                    value2: selectedServer.slaveVariables?.slaveTypeConversions
                  }
                ]}
              />
            </Flex>
          ) : (
            <Dropdown options={serverOptions} onChange={(server) => setSelectedServer(server.data)} />
          )}
        </ModalBody>

        {/* <ModalFooter>
          <Button variant='outline' mr={3} onClick={closeModal}>
            Cancel
          </Button>
          <Button colorScheme='blue' onClick={onConfirmClick}>
            Confirm
          </Button>
        </ModalFooter> */}
      </ModalContent>
    </Modal>
  )
}

export default CompareModal
