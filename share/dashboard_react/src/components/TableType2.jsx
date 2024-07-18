import { Box, Grid, GridItem, useColorMode } from '@chakra-ui/react'
import React from 'react'

function TableType2({ dataArray }) {
  const { colorMode } = useColorMode()

  return (
    <Grid templateColumns='150px auto' gap={2} p={4}>
      {dataArray.map((item, index) => (
        <React.Fragment key={index}>
          <GridItem>
            <Box p={2} borderRadius='md'>
              {item.key}
            </Box>
          </GridItem>
          <GridItem>
            <Box bg={colorMode === 'light' ? 'gray.50' : 'gray.700'} p={2} borderRadius='md'>
              {item.value}
            </Box>
          </GridItem>
        </React.Fragment>
      ))}
    </Grid>
  )
}

export default TableType2
