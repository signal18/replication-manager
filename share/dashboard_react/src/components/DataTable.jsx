import React, { useState } from 'react'
import { Table, Thead, Tbody, Tr, Th, Td, chakra, useColorMode } from '@chakra-ui/react'
import { useReactTable, flexRender, getCoreRowModel, getSortedRowModel } from '@tanstack/react-table'
import { HiOutlineSortAscending, HiOutlineSortDescending } from 'react-icons/hi'
import { useTheme } from '@emotion/react'

export function DataTable({ data, columns, fixedColumnIndex }) {
  const [sorting, setSorting] = useState([])
  const theme = useTheme()
  const { colorMode } = useColorMode()
  const table = useReactTable({
    columns,
    data,
    getCoreRowModel: getCoreRowModel(),
    onSortingChange: setSorting,
    getSortedRowModel: getSortedRowModel(),
    state: {
      sorting
    }
  })

  const styles = {
    table: {
      overflowX: 'auto',
      width: '100%'
    },
    tableColumn: {
      paddingTop: '8px',
      paddingBottom: '8px'
    },

    fixedColumn: {
      position: 'sticky',
      left: '-16px',
      zIndex: '2',
      backgroundColor: colorMode === 'light' ? theme.colors.primary.light : theme.colors.primary.dark
    }
  }

  return (
    <Table sx={styles.table}>
      <Thead>
        {table.getHeaderGroups().map((headerGroup) => (
          <Tr key={headerGroup.id}>
            {headerGroup.headers.map((header, index) => {
              const meta = header.column.columnDef.meta
              return (
                <Th
                  sx={{
                    ...styles.tableHeader,
                    ...(index === fixedColumnIndex ? styles.fixedColumn : {}),
                    width: `${header.column.columnDef.width}px`
                  }}
                  key={header.id}
                  onClick={header.column.getToggleSortingHandler()}
                  isNumeric={meta?.isNumeric}>
                  {flexRender(header.column.columnDef.header, header.getContext())}

                  <chakra.span pl='4'>
                    {header.column.getIsSorted() ? (
                      header.column.getIsSorted() === 'desc' ? (
                        <HiOutlineSortDescending aria-label='sorted descending' />
                      ) : (
                        <HiOutlineSortAscending aria-label='sorted ascending' />
                      )
                    ) : null}
                  </chakra.span>
                </Th>
              )
            })}
          </Tr>
        ))}
      </Thead>
      <Tbody>
        {table.getRowModel().rows.map((row) => (
          <Tr key={row.id}>
            {row.getVisibleCells().map((cell, index) => {
              const meta = cell.column.columnDef.meta
              return (
                <Td
                  sx={{
                    ...styles.tableColumn,
                    ...(index === fixedColumnIndex ? styles.fixedColumn : {}),
                    width: `${cell.column.columnDef.width}px`
                  }}
                  key={cell.id}
                  isNumeric={meta?.isNumeric}>
                  {flexRender(cell.column.columnDef.cell, cell.getContext())}
                </Td>
              )
            })}
          </Tr>
        ))}
      </Tbody>
    </Table>
  )
}
