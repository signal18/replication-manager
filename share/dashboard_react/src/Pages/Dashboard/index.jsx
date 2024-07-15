import React from 'react'
import Card from '../../components/Card'
import ClusterDetail from './components/ClusterDetail'
import HADetail from './components/HADetail'
import { useSelector } from 'react-redux'
import ClusterWorkload from './components/ClusterWorkload'
import { Flex, Text } from '@chakra-ui/react'
import AccordionComponent from '../../components/AccordionComponent'
import ClusterLogs from './components/Logs'
import ClusterTests from './components/ClusterTests'
import Logs from './components/Logs'
import DBServers from './components/DBServers'

function Dashboard({ selectedCluster }) {
  const {
    common: { theme, isDesktop }
  } = useSelector((state) => state)

  return (
    <Flex direction='column' gap='24px'>
      <Flex gap='24px'>
        {selectedCluster && (
          <Flex w='100%' gap='24px' direction={isDesktop ? 'row' : 'column'}>
            <ClusterDetail selectedCluster={selectedCluster} />
            <HADetail selectedCluster={selectedCluster} />
          </Flex>
        )}
      </Flex>
      <AccordionComponent
        heading={'Cluster Workload'}
        body={<ClusterWorkload workload={selectedCluster?.workLoad} />}
      />
      <AccordionComponent heading={'Databasr servers'} body={<DBServers />} />
      <AccordionComponent heading={'Cluster Logs'} body={<Logs logs={selectedCluster?.log?.buffer} />} />
      <AccordionComponent heading={'Job Logs'} body={<Logs logs={selectedCluster?.logTask?.buffer} />} />
      <AccordionComponent heading={'Tests'} body={<ClusterTests />} />
    </Flex>
  )
}

export default Dashboard
