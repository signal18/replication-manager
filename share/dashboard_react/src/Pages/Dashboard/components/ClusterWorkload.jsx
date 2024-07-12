import { Box, Flex, Text } from '@chakra-ui/react'
import React, { useState, useEffect } from 'react'
import GaugeComponent from 'react-gauge-component'
import Gauge from '../../../components/Gauge'

function ClusterWorkload({ workload }) {
  const [types, setTypes] = useState([])
  useEffect(() => {
    if (workload) {
      setTypes([
        { key: 'Queries', value: workload.qps },
        { key: 'Threads', value: workload.connections },
        { key: 'Cpu TP', value: workload.cpuThreadPool },
        { key: 'Cpu US', value: workload.cpuUserStats },
        { key: 'Tables GB', value: workload.dbTableSize / 1024 / 1024 / 1024 },
        { key: 'Indexes GB', value: workload.dbIndexSize / 1024 / 1024 / 1024 }
      ])
    }
  }, [workload])

  return (
    <Flex wrap='wrap' gap='0'>
      {types.length > 0 &&
        types.map((type, index) => {
          return <Gauge key={index} value={type.value} text={type.key} />
        })}
    </Flex>
  )
}

export default ClusterWorkload
