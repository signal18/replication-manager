import React, { useState } from 'react'
import { Table, Thead, Tbody, Tr, Th, Td, useColorMode } from '@chakra-ui/react'
import { useReactTable, flexRender, getCoreRowModel, getSortedRowModel } from '@tanstack/react-table'
import { useTheme } from '@emotion/react'

export function DataTable({ data, columns, fixedColumnIndex, enableSorting = false }) {
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
      width: '100%',
      paddingLeft: '8px'
    },
    headerRow: {
      backgroundColor: colorMode === 'light' ? `blue.100` : `blue.900`
    },
    tableHeader: {
      paddingTop: '8px',
      paddingBottom: '8px',
      paddingLeft: '4px',
      paddingRight: '4px',
      textAlign: 'center',
      border: '1px solid',
      color: theme.colors.primary.text,
      borderColor: colorMode === 'light' ? `white` : `blue.900`
    },
    tableColumn: {
      paddingTop: '8px',
      paddingBottom: '8px',
      paddingLeft: '4px',
      paddingRight: '4px',
      textAlign: 'center',
      borderRight: '1px solid',
      borderColor: colorMode === 'light' ? `blue.100` : `blue.900`,
      borderBottom: 'none'
    },
    tableColumnEven: {
      backgroundColor: colorMode === 'light' ? '#f7f8fe' : '#2A3048'
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
          <Tr key={headerGroup.id} sx={styles.headerRow}>
            {headerGroup.headers.map((header, index) => {
              const meta = header.column.columnDef.meta
              return (
                <Th
                  sx={{
                    ...styles.tableHeader,
                    ...(index === fixedColumnIndex ? styles.fixedColumn : {}),
                    maxWidth: `${header.column.columnDef.maxWidth}px`
                  }}
                  key={header.id}
                  {...(enableSorting ? { onClick: header.column.getToggleSortingHandler() } : {})}
                  isNumeric={meta?.isNumeric}>
                  {flexRender(header.column.columnDef.header, header.getContext())}

                  {{
                    asc: ' 🔼',
                    desc: ' 🔽'
                  }[enableSorting && header.column.getIsSorted()] ?? null}
                </Th>
              )
            })}
          </Tr>
        ))}
      </Thead>
      <Tbody>
        {table.getRowModel().rows.map((row, index) => (
          <Tr key={row.id} sx={index % 2 !== 0 && styles.tableColumnEven}>
            {row.getVisibleCells().map((cell, index) => {
              const meta = cell.column.columnDef.meta
              return (
                <Td
                  sx={{
                    ...styles.tableColumn,
                    ...(index === fixedColumnIndex ? styles.fixedColumn : {}),
                    maxWidth: `${cell.column.columnDef.maxWidth}px`
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
