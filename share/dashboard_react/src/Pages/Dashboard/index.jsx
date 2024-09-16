import React from 'react'
import ClusterDetail from './components/ClusterDetail'
import HADetail from './components/HADetail/index.jsx'
import { useSelector } from 'react-redux'
import ClusterWorkload from './components/ClusterWorkload'
import { Flex } from '@chakra-ui/react'
import AccordionComponent from '../../components/AccordionComponent/index.jsx'
import Logs from './components/Logs'
import DBServers from './components/DBServers'
import Proxies from './components/Proxies'
import RunTests from './components/RunTests/index.jsx'

function Dashboard({ selectedCluster, user }) {
  const {
    common: { isDesktop }
  } = useSelector((state) => state)

  return (
    <Flex direction='column' gap='8px'>
      <Flex gap='24px'>
        {selectedCluster && (
          <Flex w='100%' gap='24px' direction={isDesktop ? 'row' : 'column'}>
            <ClusterDetail selectedCluster={selectedCluster} />
            <HADetail selectedCluster={selectedCluster} />
          </Flex>
        )}
      </Flex>
      {selectedCluster?.workLoad && (
        <AccordionComponent
          heading={'Cluster Workload'}
          body={<ClusterWorkload workload={selectedCluster?.workLoad} />}
        />
      )}
      {selectedCluster && (
        <AccordionComponent
          heading={'Database servers'}
          panelSX={{ overflowX: 'auto', p: 0 }}
          body={<DBServers selectedCluster={selectedCluster} user={user} />}
        />
      )}

      <AccordionComponent
        heading={'Proxies'}
        panelSX={{ overflowX: 'auto', p: 0 }}
        body={<Proxies selectedCluster={selectedCluster} user={user} />}
      />

      <AccordionComponent heading={'Cluster Logs'} body={<Logs logs={selectedCluster?.log?.buffer} />} />
      <AccordionComponent heading={'Job Logs'} body={<Logs logs={selectedCluster?.logTask?.buffer} />} />
      <AccordionComponent heading={'Tests'} body={<RunTests selectedCluster={selectedCluster} />} />
    </Flex>
  )
}

export default Dashboard
