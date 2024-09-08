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
  rowClassName
}) {
  // const {
  //   common: { isDesktop }
  // } = useSelector((state) => state)
  return (
    <Grid templateColumns={templateColumns} className={`${styles.container} ${className}`}>
      {dataArray.map((item, index) => (
        <React.Fragment key={index}>
          <GridItem className={`${styles.row} ${rowClassName}`}>
            <Box className={`${styles.label} ${labelClassName}`}>{item.key}</Box>
          </GridItem>
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
              return (
                <React.Fragment key={subIndex}>
                  <GridItem className={`${styles.row} ${rowClassName}`}>
                    <Box className={`${styles.label} ${styles.subLabel}`} pl={3}>
                      {subItem.key}
                    </Box>
                  </GridItem>
                  <GridItem className={`${styles.row} ${rowClassName}`}>
                    <Box className={`${styles.value} ${valueClassName}`}>{subItem.value}</Box>
                  </GridItem>
                  {rowDivider && subIndex < item.value.length - 1 && (
                    <GridItem colSpan={2} className={styles.dividerRow}>
                      <Box className={styles.divider} />
                    </GridItem>
                  )}
                </React.Fragment>
              )
            })}

          {rowDivider && index < dataArray.length - 1 && (
            <GridItem colSpan={2} className={styles.dividerRow}>
              <Box className={styles.divider} />
            </GridItem>
          )}
        </React.Fragment>
      ))}
    </Grid>
  )
}

export default TableType2
