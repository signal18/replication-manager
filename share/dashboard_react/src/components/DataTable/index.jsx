import React, { useState, useEffect } from 'react'
import { Table, Thead, Tbody, Tr, Th, Td, Flex, HStack, Input, Text } from '@chakra-ui/react'
import {
  useReactTable,
  flexRender,
  getCoreRowModel,
  getSortedRowModel,
  getPaginationRowModel
} from '@tanstack/react-table'
import styles from './styles.module.scss'
import {
  HiOutlineChevronDoubleLeft,
  HiOutlineChevronDoubleRight,
  HiOutlineChevronLeft,
  HiOutlineChevronRight
} from 'react-icons/hi'
import RMIconButton from '../RMIconButton'
import Dropdown from '../Dropdown'

export function DataTable({
  data,
  columns,
  className,
  fixedColumnIndex,
  enableSorting = false,
  cellValueAlign = 'center',
  enablePagination = false
}) {
  const [sorting, setSorting] = useState([])
  const [hiddenColumns, setHiddenColumns] = useState([])
  const [pagination, setPagination] = useState({ pageIndex: 0, pageSize: 20 })
  const [pageSizeOptions, setPageSizeOptions] = useState([
    { name: 'show 10', value: 10 },
    { name: 'show 20', value: 20 },
    { name: 'show 30', value: 30 }
  ])

  const table = useReactTable({
    columns,
    data,
    getCoreRowModel: getCoreRowModel(),
    onSortingChange: setSorting,
    getPaginationRowModel: getPaginationRowModel(),
    getSortedRowModel: getSortedRowModel(),
    onPaginationChange: setPagination,
    initialState: {
      columnVisibility: {
        proxyId: false
      }
    },
    state: {
      sorting,
      ...(enablePagination ? { pagination } : {})
    }
  })

  useEffect(() => {
    setHiddenColumns(['proxyId'])
  }, [])

  return (
    <>
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
      {enablePagination && (
        <Flex className={styles.paginationContainer}>
          <HStack>
            <RMIconButton
              tooltip='Go to the first page'
              onClick={() => table.firstPage()}
              isDisabled={!table.getCanPreviousPage()}
              icon={HiOutlineChevronDoubleLeft}
            />
            <RMIconButton
              tooltip='Go to the previous page'
              onClick={() => table.previousPage()}
              isDisabled={!table.getCanPreviousPage()}
              icon={HiOutlineChevronLeft}
            />
            <RMIconButton
              tooltip='Go to the next page'
              onClick={() => table.nextPage()}
              isDisabled={!table.getCanNextPage()}
              icon={HiOutlineChevronRight}
            />
            <RMIconButton
              tooltip='Go to the last page'
              onClick={() => table.lastPage()}
              isDisabled={!table.getCanNextPage()}
              icon={HiOutlineChevronDoubleRight}
            />
          </HStack>

          <HStack>
            <HStack gap='1'>
              <Text>Page</Text>
              <strong>
                {table.getState().pagination.pageIndex + 1} of {table.getPageCount().toLocaleString()}
              </strong>
            </HStack>
            <HStack gap='1'>
              <Text width={100}>| Go to page:</Text>
              <Input
                type='number'
                width={50}
                min='1'
                max={table.getPageCount()}
                defaultValue={table.getState().pagination.pageIndex + 1}
                onChange={(e) => {
                  const page = e.target.value ? Number(e.target.value) - 1 : 0
                  table.setPageIndex(page)
                }}
              />
            </HStack>
          </HStack>
          <Dropdown
            options={pageSizeOptions}
            selectedValue={table.getState().pagination.pageSize}
            onChange={(e) => {
              table.setPageSize(Number(e.value))
            }}></Dropdown>
        </Flex>
      )}
    </>
  )
}
