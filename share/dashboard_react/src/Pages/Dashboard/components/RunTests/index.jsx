import { Flex, VStack } from '@chakra-ui/react'
import React from 'react'
import DropdownRegresssionTests from '../../../../components/DropdownRegresssionTests'
import DropdownSysbench from '../../../../components/DropdownSysbench'
import styles from './styles.module.scss'
import TableType2 from '../../../../components/TableType2'

function RunTests({ selectedCluster }) {
  const dataObject = [
    { key: 'Waiting Rejoin', value: selectedCluster?.waitingRejoin },
    { key: 'Waiting Switchover', value: selectedCluster?.waitingSwitchover },
    { key: 'Waiting Failover', value: selectedCluster?.waitingFailover }
  ]
  return (
    <Flex className={styles.testsContainer}>
      <VStack className={styles.dropdowns}>
        <DropdownRegresssionTests clusterName={selectedCluster?.name} />
        <DropdownSysbench clusterName={selectedCluster?.name} />
      </VStack>
      <TableType2 dataArray={dataObject} className={styles.table} />
    </Flex>
  )
}

export default RunTests
