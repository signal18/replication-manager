import React, { useState, useEffect } from 'react'
import { Table, Thead, Tbody, Tr, Th, Td } from '@chakra-ui/react'
import { useReactTable, flexRender, getCoreRowModel, getSortedRowModel } from '@tanstack/react-table'
import styles from './styles.module.scss'

export function DataTable({
  data,
  columns,
  className,
  fixedColumnIndex,
  enableSorting = false,
  cellValueAlign = 'center'
}) {
  const [sorting, setSorting] = useState([])
  const [hiddenColumns, setHiddenColumns] = useState([])

  const table = useReactTable({
    columns,
    data,
    getCoreRowModel: getCoreRowModel(),
    onSortingChange: setSorting,
    getSortedRowModel: getSortedRowModel(),
    initialState: {
      columnVisibility: {
        proxyId: false
      }
    },
    state: {
      sorting
    }
  })

  useEffect(() => {
    setHiddenColumns(['proxyId'])
  }, [])

  return (
    <Table className={`${styles.table} ${className}`}>
      <Thead>
        {table.getHeaderGroups().map((headerGroup) => (
          <Tr key={headerGroup.id} className={styles.headerRow}>
            {headerGroup.headers.map((header, index) => {
              const meta = header.column.columnDef.meta
              return (
                <Th
                  maxWidth={header.column.columnDef.maxWidth}
                  minWidth={header.column.columnDef.minWidth}
                  className={`${styles.tableHeader} ${index === fixedColumnIndex && styles.fixedColumn}`}
                  key={header.id}
                  {...(enableSorting ? { onClick: header.column.getToggleSortingHandler() } : {})}
                  isNumeric={meta?.isNumeric}>
                  {flexRender(header.column.columnDef.header, header.getContext())}

                  {{
                    asc: ' 🔼',
                    desc: ' 🔽'
                  }[header.column.getIsSorted()] ?? null}

                  {{
                    asc: ' 🔼',
                    desc: ' 🔽'
                  }[header.column.getIsSorted()] ?? null}

                  {{
                    asc: ' 🔼',
                    desc: ' 🔽'
                  }[header.column.getIsSorted()] ?? null}

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
        {table.getRowModel().rows.map((row, index) => {
          let rowColor = ''
          switch (row.original.state) {
            case 'SlaveErr':
            case 'SlaveLate':
            case 'Suspect':
              rowColor = 'orange'
              break
            case 'Failed':
              rowColor = 'red'
              break
          }

          return (
            <Tr
              key={row.id}
              className={`${index % 2 !== 0 && styles.tableColumnEven} ${rowColor === 'red' ? styles.redBlinking : rowColor === 'orange' ? styles.orangeBlinking : ''}`}>
              {row.getVisibleCells().map((cell, index) => {
                const meta = cell.column.columnDef.meta
                return (
                  <Td
                    textAlign={cellValueAlign}
                    maxWidth={cell.column.columnDef.maxWidth}
                    className={`${styles.tableColumn} ${index === fixedColumnIndex && styles.fixedColumn}`}
                    key={cell.id}
                    isNumeric={meta?.isNumeric}>
                    {flexRender(cell.column.columnDef.cell, cell.getContext())}
                  </Td>
                )
              })}
            </Tr>
          )
        })}
      </Tbody>
    </Table>
  )
}
