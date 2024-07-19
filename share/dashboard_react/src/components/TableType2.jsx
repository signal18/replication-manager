import { Box, Grid, GridItem, useColorMode } from '@chakra-ui/react'
import React from 'react'

function TableType2({ dataArray, sx, gap = 2, boxPadding = 2, minHeight = '40px', templateColumns = '150px auto' }) {
  const { colorMode } = useColorMode()

  return (
    <Grid templateColumns={templateColumns} gap={gap} p={2} sx={sx}>
      {dataArray.map((item, index) => (
        <React.Fragment key={index}>
          <GridItem>
            <Box p={boxPadding} borderRadius='md'>
              {item.key}
            </Box>
          </GridItem>
          <GridItem>
            <Box
              bg={colorMode === 'light' ? 'gray.50' : 'gray.700'}
              p={boxPadding}
              minHeight={minHeight}
              borderRadius='md'>
              {item.value}
            </Box>
          </GridItem>
        </React.Fragment>
      ))}
    </Grid>
  )
}

export default TableType2
