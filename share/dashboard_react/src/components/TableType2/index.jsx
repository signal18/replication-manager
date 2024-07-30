import { Box, Grid, GridItem } from '@chakra-ui/react'
import React from 'react'
import styles from './styles.module.scss'

function TableType2({
  dataArray,
  className,
  labelClassName,
  valueClassName,
  templateColumns = '150px auto',
  rowDivider = false,
  rowClassName
}) {
  return (
    <Grid templateColumns={templateColumns} className={`${styles.container} ${className}`}>
      {dataArray.map((item, index) => (
        <React.Fragment key={index}>
          <GridItem className={rowClassName}>
            <Box className={`${styles.label} ${labelClassName}`}>{item.key}</Box>
          </GridItem>
          <GridItem className={rowClassName}>
            <Box className={`${styles.value} ${valueClassName}`}>{item.value}</Box>
          </GridItem>
          {rowDivider && index < dataArray.length - 1 && (
            <GridItem colSpan={2}>
              <Box className={styles.divider} />
            </GridItem>
          )}
        </React.Fragment>
      ))}
    </Grid>
  )
}

export default TableType2
