import React, { useState, useEffect, useRef } from 'react'
import { Table, Thead, Tbody, Tr, Th, Td, Flex, HStack, Input, Text } from '@chakra-ui/react'
import {
  useReactTable,
  flexRender,
  getCoreRowModel,
  getSortedRowModel,
  getPaginationRowModel,
  getGroupedRowModel,
  getExpandedRowModel
} from '@tanstack/react-table'
import styles from './styles.module.scss'
import {
  HiArrowNarrowDown,
  HiArrowNarrowUp,
  HiOutlineChevronDoubleLeft,
  HiOutlineChevronDoubleRight,
  HiOutlineChevronLeft,
  HiOutlineChevronRight
} from 'react-icons/hi'
import RMIconButton from '../RMIconButton'
import Dropdown from '../Dropdown'
import { getColorFromServerStatus } from '../../utility/common'

/** 
 * @param {{ data: any[], columns: any[], columnVisibility?: import ('@tanstack/react-table').ColumnVisibilityState, initialGrouping: string[], initialExpanded: any, className?: string, fixedColumnIndex?: number, enableSorting?: boolean, cellValueAlign?: 'left' | 'center' | 'right', enablePagination?: boolean, enableGrouping?: boolean, enableExpanding?: boolean }} props
 **/
export const DataTable = React.memo(function DataTable({
  data,
  columns,
  columnVisibility = { proxyId: false, groupHeader: false },
  initialGrouping = [],
  initialExpanded = {},
  className,
  fixedColumnIndex,
  enableSorting = false,
  cellValueAlign = 'center',
  enablePagination = false,
  enableGrouping = false,
  enableExpanding = false,
}) {
  const [sorting, setSorting] = useState([])
  const [grouping, setGrouping] = useState(initialGrouping)
  const [expanded, setExpanded] = useState(initialExpanded)

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
    enableGrouping: enableGrouping,
    enableExpanding: enableExpanding,
    getGroupedRowModel: getGroupedRowModel(),
    getExpandedRowModel: getExpandedRowModel(),
    onGroupingChange: setGrouping,
    onSortingChange: setSorting,
    onExpandedChange: setExpanded,
    enableSortingRemoval: false,
    getSortedRowModel: getSortedRowModel(),
    ...(enablePagination ? { getPaginationRowModel: getPaginationRowModel(), onPaginationChange: setPagination } : {}),
    initialState: {
      columnVisibility: columnVisibility,
      expanded: initialExpanded,
      grouping: initialGrouping,
    },
    state: {
      sorting,
      grouping,
      expanded,
      ...(enablePagination ? { pagination } : {})
    }
  })

  return (
    <>
      <Table className={`${styles.table} ${className}`}>
        <Thead>
          {table.getHeaderGroups().map((headerGroup) => {
            return (
              <Tr key={headerGroup.id} className={styles.headerRow}>
                {headerGroup.headers.map((header, index) => {
                  const meta = header.column.columnDef.meta
                  const isColumnSortable = header.column.columnDef.enableSorting
                  return (
                    <Th
                      colSpan={header.colSpan}
                      maxWidth={header.column.columnDef.maxWidth}
                      minWidth={header.column.columnDef.minWidth}
                      width={header.column.columnDef.width}
                      textAlign={header.column.columnDef.textAlign}
                      className={`${styles.tableHeader} ${index === fixedColumnIndex && styles.fixedColumn} ${isColumnSortable && styles.sortableColumn} ${header.column.parent && styles.groupedColumn}`}
                      key={header.id}
                      {...(enableSorting ? { onClick: header.column.getToggleSortingHandler() } : {})}
                      isNumeric={meta?.isNumeric}>
                      {header.isPlaceholder ? null : flexRender(header.column.columnDef.header, header.getContext())}

                      {{
                        asc: <HiArrowNarrowUp className={styles.sortIcon} />,
                        desc: <HiArrowNarrowDown className={styles.sortIcon} />
                      }[enableSorting && header.column.getIsSorted()] ?? null}
                    </Th>
                  )
                })}
              </Tr>
            )
          })}
        </Thead>
        <Tbody>
          {table.getRowModel().rows.length === 0 ? (
            <Tr className={styles.nodataRow}>
              <Td className={styles.tableColumn} colSpan={table.getHeaderGroups()[0].headers.length}>
                No data found
              </Td>
            </Tr>
          ) : (
            table.getRowModel().rows.map((row, index) => {
              if (row.getIsGrouped()) {
                return (<GroupHeaderRow key={row.id} row={row} table={table} colSpan={row.getVisibleCells().length} fixedColumnIndex={fixedColumnIndex} cellValueAlign={cellValueAlign} />)
              } else {
                return (<DataRow key={row.id} row={row} index={index} fixedColumnIndex={fixedColumnIndex} cellValueAlign={cellValueAlign} />)
              }
            })
          )}
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
})

/**
 * @param {{ row: import('@tanstack/react-table').Row<any>, table: import('@tanstack/react-table').Table<any>, colSpan: number }} props
 */
function GroupHeaderRow({ row, table, colSpan, fixedColumnIndex = 0, cellValueAlign = 'left' }) {
  const groupHeaderCol = table.getAllColumns().find(col => col.id === 'groupHeader');
  const renderFn = groupHeaderCol?.columnDef?.meta?.renderGroupHeader;
  const renderMenuFn = groupHeaderCol?.columnDef?.meta?.renderGroupHeaderMenu;
  const newColSpan = renderMenuFn ? colSpan - 1 : colSpan;

  return (
    <>
      <Tr key={row.id} className={`${styles.tableColumn}`} >
        { renderMenuFn && (<Td>{renderMenuFn(row)}</Td>) }
        <Td colSpan={newColSpan} onClick={row.getToggleExpandedHandler()} style={{ cursor: 'pointer' }}>
          {renderFn
            ? renderFn(row)
            : <>{row.original.groupName}</>}
        </Td>
      </Tr>
      { row.subRows.map((subRow, index) => {
      <DataRow key={`${row.id}-${subRow.id}`} row={subRow} index={index} fixedColumnIndex={fixedColumnIndex} cellValueAlign={cellValueAlign} />
      })}
    </>
  );
}

/**
 * @param {{ row: import('@tanstack/react-table').Row<any>, index: number, fixedColumnIndex?: number, cellValueAlign?: 'left' | 'center' | 'right' }} props
 */
function DataRow({ row, index, fixedColumnIndex = 0, cellValueAlign = 'left' }) {
  //this logic is for db servers
  let rowColor = row.original.state ? getColorFromServerStatus(row.original.state) : row.original.rowColor ? row.original.rowColor : ''

  //This logic is for process list
  if (row.original.command?.toLowerCase() === 'query') {
    if (row.original.time?.Float64 > 1) {
      rowColor = 'orange'
    } else if (row.original?.time.Float64 > 10) {
      rowColor = 'red'
    }
  }

  return (
    <Tr
      key={row.id}
      className={`${index % 2 !== 0 && styles.tableColumnEven} ${rowColor === 'red' ? styles.redBlinking : rowColor === 'orange' ? styles.orangeBlinking : ''}`}>
      {row.getVisibleCells().map((cell, index) => {
        const meta = cell.column.columnDef.meta
        return (
          <Td
            textAlign={cell.column.columnDef.textAlign || cellValueAlign}
            maxWidth={cell.column.columnDef.maxWidth}
            width={cell.column.columnDef.width}
            className={`${styles.tableColumn} ${index === fixedColumnIndex && styles.fixedColumn}`}
            key={cell.id}
            isNumeric={meta?.isNumeric}>
            {flexRender(cell.column.columnDef.cell, cell.getContext())}
          </Td>
        )
      })}
    </Tr>
  )
}