import { Box, Grid, GridItem } from '@chakra-ui/react'
import React from 'react'
import { useSelector } from 'react-redux'

function TableType2({ dataArray }) {
  const {
    common: { theme }
  } = useSelector((state) => state)

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
            <Box bg={theme === 'light' ? 'gray.50' : 'gray.700'} p={2} borderRadius='md'>
              {item.value}
            </Box>
          </GridItem>
        </React.Fragment>
      ))}
    </Grid>
  )
}

export default TableType2
