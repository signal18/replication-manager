import {
  Box,
  Flex,
  HStack,
  IconButton,
  SimpleGrid,
  Spacer,
  Stack,
  Text,
  Tooltip,
  useColorMode,
  VStack
} from '@chakra-ui/react'
import { flexRender, getCoreRowModel, useReactTable } from '@tanstack/react-table'
import React from 'react'
import Card from '../../../../components/Card'
import { useSelector } from 'react-redux'
import MenuOptions from '../../../../components/MenuOptions'
import ServerMenu from './ServerMenu'
import { HiTable } from 'react-icons/hi'
import { MdCompare } from 'react-icons/md'
import TableType3 from '../../../../components/TableType3'

function DBServersGrid({ data, columns, clusterMasterId, clusterName, user, showTableView }) {
  const {
    common: { isDesktop, isTablet, isMobile }
  } = useSelector((state) => state)
  const { colorMode } = useColorMode()
  const table = useReactTable({
    columns,
    data,
    getCoreRowModel: getCoreRowModel()
  })

  const styles = {
    card: {
      borderRadius: '16px',
      border: '1px solid',
      width: '100%',
      borderColor: colorMode === 'light' ? `blue.100` : `blue.800`
    },

    header: {
      textAlign: 'center',
      p: '16px',
      bg: colorMode === 'light' ? `blue.100` : `blue.800`,
      borderTopLeftRadius: '16px',
      borderTopRightRadius: '16px',
      color: '#000',
      fontWeight: 'bold'
    }
  }

  return (
    <SimpleGrid columns={{ base: 1, sm: 2, md: 2, lg: 3 }} spacing={2} marginTop='24px'>
      {table?.getRowModel()?.rows?.map((row) => {
        const tableData1 = [
          { key: 'Status', value: row.getValue('status') },
          { key: 'In Maintenance', value: row.getValue('inMaintenance') },
          { key: 'Prefered / Ignored', value: row.getValue('prfIgn') },
          { key: 'Read Only', value: row.getValue('roSts') },
          { key: 'Ignore Read Only', value: row.getValue('ignRO') },
          { key: 'Event Scheduler', value: row.getValue('evtSch') }
        ]
        const tableData2 = [
          { key: 'Status', value: row.getValue('status') },
          { key: 'In Maintenance', value: row.getValue('inMaintenance') },
          { key: 'Prefered / Ignored', value: row.getValue('prfIgn') },
          { key: 'Read Only', value: row.getValue('roSts') },
          { key: 'Ignore Read Only', value: row.getValue('ignRO') },
          { key: 'Event Scheduler', value: row.getValue('evtSch') }
        ]
        return (
          <VStack width='100%' key={row.id} sx={styles.card}>
            <Flex as='header' width='100%' sx={styles.header} align='center'>
              {row.getValue('dbFlavor')}
              {row.getValue('serverName')}
              <Spacer />
              <Tooltip label='Compare servers'>
                <IconButton icon={<MdCompare />} size='sm' fontSize='1.5rem' marginRight={2} />
              </Tooltip>
              <Tooltip label='Show table view'>
                <IconButton icon={<HiTable />} onClick={showTableView} size='sm' fontSize='1.5rem' marginRight={2} />
              </Tooltip>
              <ServerMenu
                clusterMasterId={clusterMasterId}
                row={row.original}
                clusterName={clusterName}
                isDesktop={isDesktop}
                user={user}
              />
            </Flex>
            <Flex direction='column' width='100%' mb={2}>
              <TableType3 dataArray={tableData1} />
            </Flex>
          </VStack>
        )
      })}
    </SimpleGrid>
  )
}

export default DBServersGrid
