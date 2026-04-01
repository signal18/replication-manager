import { Box, Grid, GridItem } from '@chakra-ui/react'
import React from 'react'
import styles from './styles.module.scss'

function TableType2({
  dataArray,
  className,
  labelClassName,
  valueClassName,
  templateColumns = '150px auto',
  rowDivider = true,
  rowClassName,
  helpColumn = false
}) {
  const cols = helpColumn
    ? templateColumns.replace(/^(\S+)\s+(.+)$/, '$1 32px $2')
    : templateColumns
  const span = helpColumn ? 3 : 2

  const renderHelpCell = (help, sub = false) => helpColumn ? (
    <GridItem className={`${styles.row} ${rowClassName}`}>
      <Box className={sub ? styles.helpCellSub : styles.helpCell}>
        {help || null}
      </Box>
    </GridItem>
  ) : null

  return (
    <Grid templateColumns={cols} className={`${styles.container} ${className}`}>
      {dataArray.map((item, index) => (
        <React.Fragment key={index}>
          <GridItem className={`${styles.row} ${rowClassName}`}>
            <Box className={`${styles.label} ${labelClassName}`}>{item.key}</Box>
          </GridItem>
          {renderHelpCell(item.help)}
          {Array.isArray(item.value) ? (
            <GridItem className={`${styles.row} ${rowClassName}`}>
              <Box className={`${styles.label} ${labelClassName}`}></Box>
            </GridItem>
          ) : (
            <GridItem className={`${styles.row} ${rowClassName}`}>
              <Box className={`${styles.value} ${valueClassName}`}>{item.value}</Box>
            </GridItem>
          )}
          {Array.isArray(item.value) &&
            item.value.map((subItem, subIndex) => {
              const isFullWidth = subItem.fullWidth
              return (
                <React.Fragment key={subIndex}>
                  {isFullWidth ? (
                    <>
                      {subItem.key && (
                        <GridItem colSpan={span} className={`${styles.row} ${rowClassName}`}>
                          <Box className={`${styles.label} ${styles.subLabel}`} pl={3}>
                            {subItem.key}
                          </Box>
                        </GridItem>
                      )}
                      <GridItem colSpan={span} className={`${styles.row} ${rowClassName}`}>
                        <Box className={`${styles.value} ${valueClassName}`}>{subItem.value}</Box>
                      </GridItem>
                    </>
                  ) : (
                    <>
                      <GridItem className={`${styles.row} ${rowClassName}`}>
                        <Box className={`${styles.label} ${styles.subLabel}`} pl={3}>
                          {subItem.key}
                        </Box>
                      </GridItem>
                      {renderHelpCell(subItem.help, true)}
                      <GridItem className={`${styles.row} ${rowClassName}`}>
                        <Box className={`${styles.value} ${valueClassName}`}>{subItem.value}</Box>
                      </GridItem>
                    </>
                  )}
                  {rowDivider && subIndex < item.value.length - 1 && (
                    <GridItem colSpan={span} className={styles.dividerRow}>
                      <Box className={styles.divider} />
                    </GridItem>
                  )}
                </React.Fragment>
              )
            })}
          {rowDivider && index < dataArray.length - 1 && (
            <GridItem colSpan={span} className={styles.dividerRow}>
              <Box className={styles.divider} />
            </GridItem>
          )}
        </React.Fragment>
      ))}
    </Grid>
  )
}

export default TableType2
