import { Grid, GridItem, Text } from '@chakra-ui/react'
import React from 'react'

function TableType1({ dataArray }) {
  return (
    <Grid templateColumns='repeat(2,1fr)' px='4'>
      {dataArray.map((data, index) => {
        const isLast = dataArray.length - 1 === index
        const borderBottom = { ...(!isLast ? { borderBottom: '1px solid gray' } : {}) }
        return (
          <React.Fragment key={index}>
            <GridItem {...borderBottom}>
              <Text fontSize='14px' p='1' fontWeight='bold'>
                {data.key}
              </Text>
            </GridItem>
            <GridItem p='1' {...borderBottom}>
              {data.value}
            </GridItem>
          </React.Fragment>
        )
      })}
    </Grid>
  )
}

export default TableType1
