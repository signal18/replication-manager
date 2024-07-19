import React from 'react'
import { Box, Table, TableContainer, Tbody, Td, Th, Thead, Tr, useColorMode } from '@chakra-ui/react'

function TableType3({ dataArray }) {
  const { colorMode } = useColorMode()

  const styles = {
    tableContainer: {
      width: '97%',
      borderRadius: '16px',
      border: '2px solid',
      margin: 'auto',
      borderColor: colorMode === 'light' ? `blue.100` : `blue.900`,
      overflow: 'hidden'
    },
    table: {
      fontSize: '10px'
    },

    column: {
      padding: '0',
      whiteSpace: 'break-spaces',
      borderLeft: '2px solid',
      borderColor: colorMode === 'light' ? `blue.100` : `blue.900`,
      textAlign: 'center'
    },
    bodyColumn: {
      lineHeight: '0'
    }
  }
  return (
    <Box sx={styles.tableContainer}>
      <Table variant='simple' sx={styles.table}>
        <Thead>
          <Tr>
            {dataArray.map((dataItem, index) => (
              <Th key={index} sx={styles.column}>
                {dataItem.key}
              </Th>
            ))}
          </Tr>
        </Thead>
        <Tbody>
          <Tr>
            {dataArray.map((dataItem, index) => (
              <Td key={index} sx={{ ...styles.column, ...styles.bodyColumn }}>
                {dataItem.value}
              </Td>
            ))}
          </Tr>
        </Tbody>
      </Table>
    </Box>
  )
}

export default TableType3
