import { Flex, HStack, Text } from '@chakra-ui/react'
import React from 'react'
import Card from '../../components/Card'
import TagPill from '../../components/TagPill'
import ClusterDetail from './components/ClusterDetail'
import HADetail from './components/HADetail'

function Dashboard({ selectedCluster }) {
  return (
    <Flex direction='column' gap='24px'>
      <Flex gap='24px'>
        {selectedCluster && (
          <>
            <ClusterDetail selectedCluster={selectedCluster} />
            <HADetail selectedCluster={selectedCluster} />
          </>
        )}
      </Flex>
      <Card header={<Text>Cluster Workload</Text>}></Card>
      <Card header={<Text>Cluster Logs</Text>}></Card>
      <Card header={<Text>Tests</Text>}></Card>
    </Flex>
  )
}

export default Dashboard
