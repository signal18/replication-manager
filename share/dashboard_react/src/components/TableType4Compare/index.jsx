import { Table, Box, Thead, Tr, Th, Tbody, Td } from '@chakra-ui/react'
import React from 'react'
import styles from './styles.module.scss'

function TableType4Compare({ item1Title, item2Title, dataArray }) {
  return (
    <Box className={styles.tableContainer}>
      <Table variant='striped' className={styles.table}>
        <Thead className={styles.header}>
          <Tr>
            <Th className={styles.column}></Th>
            <Th className={styles.column}>{item1Title}</Th>
            <Th className={styles.column}>{item2Title}</Th>
          </Tr>
        </Thead>
        <Tbody>
          {dataArray.map((dataItem, index) => (
            <Tr key={index} className={styles.row}>
              <Td className={styles.column}>{dataItem.key}</Td>
              <Td className={styles.column}>{dataItem.value1}</Td>
              <Td className={styles.column}>{dataItem.value2}</Td>
            </Tr>
          ))}
        </Tbody>
      </Table>
    </Box>
  )
}

export default TableType4Compare
