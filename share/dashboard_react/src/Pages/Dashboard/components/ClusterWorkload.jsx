import { Flex } from '@chakra-ui/react'
import React from 'react'
import Gauge from '../../../components/Gauge'

function ClusterWorkload({ workload }) {
  return (
    <Flex wrap='wrap' gap='0' align='center' justify='space-evenly'>
      <Gauge minValue={0} maxValue={300000} value={workload?.qps} text={'Queries'} width={150} height={90} />
      <Gauge minValue={0} maxValue={300000} value={workload?.connections} text={'Threads'} width={150} height={90} />
      <Gauge minValue={0} maxValue={100} value={workload?.cpuThreadPool} text={'Cpu TP'} width={150} height={90} />
      <Gauge minValue={0} maxValue={100} value={workload?.cpuUserStats} text={'Cpu US'} width={150} height={90} />
      <Gauge
        minValue={0}
        maxValue={10000}
        value={workload?.dbTableSize / 1024 / 1024 / 1024}
        text={'Tables GB'}
        width={150}
        height={90}
      />
      <Gauge
        minValue={0}
        maxValue={10000}
        value={workload?.dbIndexSize / 1024 / 1024 / 1024}
        text={'Indexes GB'}
        width={150}
        height={90}
      />
    </Flex>
  )
}

export default ClusterWorkload
