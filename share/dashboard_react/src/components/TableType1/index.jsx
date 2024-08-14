import { Divider, Grid, GridItem, Text } from '@chakra-ui/react'
import React from 'react'
import styles from './styles.module.scss'

function TableType1({ dataArray }) {
  return (
    <Grid templateColumns='repeat(2,1fr)' className={styles.container}>
      {dataArray.map((data, index) => {
        const isLast = dataArray.length - 1 === index
        return (
          <React.Fragment key={index}>
            <GridItem className={styles.gridItem}>
              <Text className={styles.label}>{data.key}</Text>
            </GridItem>
            <GridItem className={styles.gridItem}>
              <Text className={styles.value}>{data.value}</Text>
            </GridItem>
            {!isLast && (
              <GridItem colSpan={2}>
                <Divider />
              </GridItem>
            )}
          </React.Fragment>
        )
      })}
    </Grid>
  )
}

export default TableType1
