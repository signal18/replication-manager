import { Table, Box, Thead, Tr, Th, Tbody, Td, useColorMode } from '@chakra-ui/react'
import { useTheme } from '@emotion/react'
import React from 'react'

function TableType4Compare({ item1Title, item2Title, dataArray }) {
  const { colorMode } = useColorMode()
  const theme = useTheme()
  const styles = {
    tableContainer: {
      borderRadius: '16px',
      overflow: 'auto',
      border: '2px solid',
      borderColor: colorMode === 'light' ? `blue.100` : `blue.800`
    },

    header: {
      height: '48px',
      bg: colorMode === 'light' ? `blue.100` : `blue.800`,
      color: colorMode === 'light' ? theme.colors.primary.light : theme.colors.primary.dark,
      overflow: 'hidden',
      borderBottom: '2px solid',
      borderColor: colorMode === 'light' ? `blue.100` : `blue.800`,
      position: 'sticky',
      top: '0'
    },

    column: {
      whiteSpace: 'break-spaces',
      padding: '8px',
      wordBreak: 'break-all'
    }
  }
  return (
    <Box sx={styles.tableContainer}>
      <Table variant='striped' sx={styles.table}>
        <Thead sx={styles.header}>
          <Tr>
            <Th sx={styles.column}></Th>
            <Th sx={styles.column}>{item1Title}</Th>
            <Th sx={styles.column}>{item2Title}</Th>
          </Tr>
        </Thead>
        <Tbody>
          {dataArray.map((dataItem, index) => (
            <Tr key={index}>
              <Td sx={{ ...styles.column }}>{dataItem.key}</Td>
              <Td sx={{ ...styles.column }}>{dataItem.value1}</Td>
              <Td sx={{ ...styles.column }}>{dataItem.value2}</Td>
            </Tr>
          ))}
        </Tbody>
      </Table>
    </Box>
  )
}

export default TableType4Compare
