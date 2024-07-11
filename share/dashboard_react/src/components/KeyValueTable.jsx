import React from 'react'
import { useTable } from 'react-table'
import { Table, Tbody, Tr, Td } from '@chakra-ui/react'

function KeyValuesTable({ data }) {
  const columns = React.useMemo(
    () => [
      {
        accessor: 'key'
      },
      {
        accessor: 'value'
      }
    ],
    []
  )

  const { getTableProps, getTableBodyProps, rows, prepareRow } = useTable({ columns, data })

  return (
    <Table {...getTableProps()} variant='simple' size='sm' mt='3'>
      <Tbody {...getTableBodyProps()}>
        {rows.map((row) => {
          prepareRow(row)
          return (
            <Tr {...row.getRowProps()} style={{ borderBottom: '1px solid grey' }}>
              {row.cells.map((cell) => {
                return <Td {...cell.getCellProps()}>{cell.render('Cell')}</Td>
              })}
            </Tr>
          )
        })}
      </Tbody>
    </Table>
  )
}

export default KeyValuesTable
