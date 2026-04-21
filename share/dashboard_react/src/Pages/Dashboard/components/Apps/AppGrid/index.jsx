import { Flex, SimpleGrid, Spacer, VStack, Text, Box } from '@chakra-ui/react'
import React from 'react'
import AppMenu from '../AppMenu'
import { HiTable } from 'react-icons/hi'
import TableType2 from '../../../../../components/TableType2'
import AccordionComponent from '../../../../../components/AccordionComponent'
import AppStatus from '../AppStatus'
import ServerStatus from '../../../../../components/ServerStatus'
import TagPill from '../../../../../components/TagPill'
import RMIconButton from '../../../../../components/RMIconButton'
import styles from './styles.module.scss'
import ServerName from '../../../../../components/ServerName'

function AppGrid({ apps = [], clusterName, showTableView, user, isDesktop, orchestrator }) {
  return (
    <SimpleGrid columns={{ base: 1, sm: 1, md: 2, lg: 3 }} spacing={2} spacingY={6} spacingX={6} marginTop='4px'>
      {apps?.length > 0 &&
        apps.map((rowData) => {
          const appData = [
            {
              key: 'Id',
              value: rowData.id
            },
            {
              key: 'Status',
              value: <AppStatus status={rowData.state} />
            },
            {
              key: 'Version',
              value: rowData.version
            }
          ]
          return (
            <VStack width='100%' key={rowData.id} className={styles.card}>
              <Flex as='header' width='100%' align='center' className={styles.header}>
                <ServerName as='p' name={`${rowData.host}:${rowData.port}`} className={styles.serverName} />
                <Spacer />

                <RMIconButton icon={HiTable} onClick={showTableView} marginRight={2} tooltip='Show table view' />  
                  <AppMenu
                    from='gridView'
                    row={rowData}
                    clusterName={clusterName}
                    isDesktop={isDesktop}
                    user={user}
                    orchestrator={orchestrator}
                  />
              </Flex>

              <Flex direction='column' width='100%' mb={2} gap='0'>
                <TableType2
                  dataArray={appData}
                  templateColumns='30% auto'
                  className={styles.table}
                  labelClassName={styles.rowLabel}
                  valueClassName={styles.rowValue}
                />
              </Flex>
            </VStack>
          )
        })}
    </SimpleGrid>
  )
}

export default AppGrid
