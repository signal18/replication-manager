import React from 'react'
import { Box, Table, Tbody, Td, Th, Thead, Tr } from '@chakra-ui/react'
import styles from './styles.module.scss'

function TableType3({ dataArray, isBlocking, color }) {
  return (
    <Box className={`${styles.tableContainer} ${isBlocking ? styles[color] : ''}`}>
      <Table variant='simple' className={styles.table}>
        <Thead>
          <Tr>
            {dataArray.map((dataItem, index) => (
              <Th key={index} className={`${styles.column}  ${isBlocking ? styles[color] : ''}`}>
                {dataItem.key}
              </Th>
            ))}
          </Tr>
        </Thead>
        <Tbody>
          <Tr>
            {dataArray.map((dataItem, index) => (
              <Td key={index} className={`${styles.column} ${styles.bodyColumn}  ${isBlocking ? styles[color] : ''}`}>
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
