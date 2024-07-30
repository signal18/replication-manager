import { Flex, SimpleGrid, Spacer, Tooltip, useColorMode, VStack, Text, Box } from '@chakra-ui/react'
import React from 'react'
import ProxyMenu from '../ProxyMenu'
import { HiTable } from 'react-icons/hi'
import ProxyLogo from '../ProxyLogo'
import TableType2 from '../../../../../components/TableType2'
import AccordionComponent from '../../../../../components/AccordionComponent'
import ProxyStatus from '../ProxyStatus'
import ServerStatus from '../../../../../components/ServerStatus'
import TagPill from '../../../../../components/TagPill'
import IconButton from '../../../../../components/IconButton'
import cssStyles from './styles.module.scss'

function ProxyGrid({ proxies, clusterName, showTableView, user, isDesktop, isMenuOptionsVisible }) {
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

                <IconButton icon={HiTable} onClick={showTableView} marginRight={2} tooltip='Show table view' />
                {isMenuOptionsVisible && (
                  <ProxyMenu
                    from='gridView'
                    row={rowData}
                    clusterName={clusterName}
                    isDesktop={isDesktop}
                    user={user}
                  />
                )}
              </Flex>

              <Flex direction='column' width='100%' mb={2} gap='0'>
                <TableType2 dataArray={proxyData} templateColumns='30% auto' />
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
                      headerClassName={styles.accordionHeader}
                      panelClassName={styles.accordionPanel}
                      body={<TableType2 dataArray={readWriteTableData} templateColumns='30% auto' />}
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
