import { Flex, SimpleGrid, Spacer, VStack, Text, Box } from '@chakra-ui/react'
import React from 'react'
import ProxyMenu from '../ProxyMenu'
import { HiTable } from 'react-icons/hi'
import ProxyLogo from '../ProxyLogo'
import TableType2 from '../../../../../components/TableType2'
import AccordionComponent from '../../../../../components/AccordionComponent'
import ProxyStatus from '../ProxyStatus'
import ServerStatus from '../../../../../components/ServerStatus'
import TagPill from '../../../../../components/TagPill'
import RMIconButton from '../../../../../components/RMIconButton'
import styles from './styles.module.scss'
import ServerName from '../../../../../components/ServerName'

function ProxyGrid({ proxies, clusterName, showTableView, user, isDesktop, isMenuOptionsVisible }) {
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
            <VStack width='100%' key={rowData.id} className={styles.card}>
              <Flex as='header' width='100%' align='center' className={styles.header}>
                <ProxyLogo proxyName={rowData.type} />
                <ServerName as='p' name={`${rowData.host}:${rowData.port}`} className={styles.serverName} />
                <Spacer />

                <RMIconButton icon={HiTable} onClick={showTableView} marginRight={2} tooltip='Show table view' />
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
                <TableType2
                  dataArray={proxyData}
                  templateColumns='30% auto'
                  className={styles.table}
                  labelClassName={styles.rowLabel}
                  valueClassName={styles.rowValue}
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
                          <Text className=''>{`${object.data.host}:${object.data.port}`}</Text>
                        </Flex>
                      }
                      headerClassName={styles.accordionHeader}
                      panelClassName={styles.accordionPanel}
                      body={
                        <TableType2
                          dataArray={readWriteTableData}
                          templateColumns='30% auto'
                          className={styles.table}
                          labelClassName={styles.rowLabel}
                          valueClassName={styles.rowValue}
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
