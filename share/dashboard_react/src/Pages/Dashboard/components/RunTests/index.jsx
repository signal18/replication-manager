import { Flex, VStack } from '@chakra-ui/react'
import React, { useState } from 'react'
import DropdownRegresssionTests from '../../../../components/DropdownRegresssionTests'
import DropdownSysbench from '../../../../components/DropdownSysbench'
import styles from './styles.module.scss'
import TableType2 from '../../../../components/TableType2'
import RMButton from '../../../../components/RMButton'
import BenchCompareModal from '../../../../components/Modals/BenchCompareModal'

function RunTests({ selectedCluster }) {
  const [isBenchCompareOpen, setIsBenchCompareOpen] = useState(false)

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
        <RMButton size='small' colorScheme='purple' onClick={() => setIsBenchCompareOpen(true)}>
          Compare Benchmarks
        </RMButton>
      </VStack>
      <TableType2 dataArray={dataObject} className={styles.table} />

      {isBenchCompareOpen && (
        <BenchCompareModal
          isOpen={isBenchCompareOpen}
          closeModal={() => setIsBenchCompareOpen(false)}
          clusterName={selectedCluster?.name}
        />
      )}
    </Flex>
  )
}

export default RunTests
