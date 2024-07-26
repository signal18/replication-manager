import { Box, Grid, GridItem } from '@chakra-ui/react'
import React from 'react'
import styles from './styles.module.scss'

function TableType2({ dataArray, sx, gap = 2, boxPadding = 2, minHeight = '40px', templateColumns = '150px auto' }) {
  return (
    <Grid templateColumns={templateColumns} gap={gap} p={2} className={styles.container} sx={sx}>
      {dataArray.map((item, index) => (
        <React.Fragment key={index}>
          <GridItem>
            <Box p={boxPadding} borderRadius='md'>
              {item.key}
            </Box>
          </GridItem>
          <GridItem>
            <Box className={styles.valueContainer} p={boxPadding} minHeight={minHeight}>
              {item.value}
            </Box>
          </GridItem>
        </React.Fragment>
      ))}
    </Grid>
  )
}

export default TableType2
