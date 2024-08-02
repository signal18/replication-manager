import { Flex, GridItem, Heading } from '@chakra-ui/react'
import React from 'react'
import styles from './styles.module.scss'

function GridItemContainer({ title, children }) {
  return (
    <GridItem className={styles.gridItem}>
      <Heading className={styles.heading}>{title}</Heading>
      <Flex className={styles.body}>{children}</Flex>
    </GridItem>
  )
}

export default GridItemContainer
