import { Flex, VStack } from '@chakra-ui/react'
import React from 'react'
import DropdownRegresssionTests from '../../../../components/DropdownRegresssionTests'
import DropdownSysbench from '../../../../components/DropdownSysbench'

function RunTests({ selectedCluster }) {
  return (
    <VStack>
      <Flex>
        {/* <DropdownRegresssionTests />
        <DropdownSysbench /> */}
      </Flex>
    </VStack>
  )
}

export default RunTests
