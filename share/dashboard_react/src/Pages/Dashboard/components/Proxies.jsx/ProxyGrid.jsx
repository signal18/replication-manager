import { Flex, IconButton, SimpleGrid, Spacer, Tooltip, useColorMode, VStack, Text, Box } from '@chakra-ui/react'
import React from 'react'
import ProxyMenu from './ProxyMenu'
import { HiTable } from 'react-icons/hi'
import ProxyLogo from './ProxyLogo'
import TableType2 from '../../../../components/TableType2'
import AccordionComponent from '../../../../components/AccordionComponent'
import ProxyStatus from './ProxyStatus'
import ServerStatus from '../../../../components/ServerStatus'
import TagPill from '../../../../components/TagPill'

function ProxyGrid({ proxies, clusterName, showTableView, user, isDesktop }) {
  const { colorMode } = useColorMode()
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
    tableType2: {
      padding: '0.5',
      marginTop: '2',
      fontSize: '15px',
      width: '100%'
    },
    accordionHeader: {
      borderRadius: '0',
      padding: '6px',
      fontSize: '15px'
    },
    accordionPanel: {
      borderRadius: '0',
      border: 'none'
    }
  }

  return (
    <SimpleGrid columns={{ base: 1, sm: 1, md: 2, lg: 3 }} spacing={2} spacingY={6} spacingX={6} marginTop='4px'>
      {proxies?.length > 0 &&
        proxies.map((rowData) => {
          const proxyData = [
            {
              key: 'Id',
              value: rowData.id
            },
            {
              key: 'Status',
              value: <ProxyStatus status={rowData.state} />
            },
            {
              key: 'Version',
              value: rowData.version
            }
          ]
          let readWriteData = []
          rowData.backendsWrite?.forEach((writeData) => {
            readWriteData.push({ type: 'write', data: writeData })
          })
          rowData.backendsRead?.forEach((readData) => {
            readWriteData.push({ type: 'read', data: readData })
          })

          return (
            <VStack width='100%' key={rowData.id} sx={{ ...styles.card }}>
              <Flex as='header' width='100%' align='center' sx={styles.header}>
                <ProxyLogo proxyName={rowData.type} />
                <Text margin='auto' w='100%'>{`${rowData.host}:${rowData.port}`}</Text>
                <Spacer />
                <Tooltip label='Show table view'>
                  <IconButton icon={<HiTable />} onClick={showTableView} size='sm' fontSize='1.5rem' marginRight={2} />
                </Tooltip>
                <ProxyMenu from='gridView' row={rowData} clusterName={clusterName} isDesktop={isDesktop} user={user} />
              </Flex>

              <Flex direction='column' width='100%' mb={2} gap='0'>
                <TableType2
                  dataArray={proxyData}
                  templateColumns='30% auto'
                  gap={1}
                  boxPadding={1}
                  minHeight='24px'
                  sx={styles.tableType2}
                />
                {readWriteData?.map((object) => {
                  const readWriteTableData = [
                    {
                      key: 'PX Status',
                      value: object.data.prxStatus
                    },
                    {
                      key: 'Connections',
                      value: object.data.prxConnections
                    },
                    {
                      key: 'Bytes Out',
                      value: object.data.prxByteOut
                    },
                    {
                      key: 'Bytes In',
                      value: object.data.prxByteIn
                    },
                    {
                      key: 'Sess Time',
                      value: object.data.prxLatency
                    },
                    {
                      key: 'Id Group',
                      value: object.data.prxHostgroup
                    }
                  ]
                  return (
                    <AccordionComponent
                      heading={
                        <Flex gap='2'>
                          <TagPill text={object.type.toUpperCase()} />
                          <ServerStatus state={object.data.status} />
                          <Text>{`${object.data.host}:${object.data.port}`}</Text>
                        </Flex>
                      }
                      headerSX={styles.accordionHeader}
                      panelSX={styles.accordionPanel}
                      body={
                        <TableType2
                          dataArray={readWriteTableData}
                          templateColumns='30% auto'
                          gap={1}
                          boxPadding={1}
                          minHeight='24px'
                          sx={styles.tableType2}
                        />
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

export default ProxyGrid
