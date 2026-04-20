import React from 'react'
import ClusterDetail from './components/ClusterDetail'
import HADetail from './components/HADetail/index.jsx'
import { useSelector } from 'react-redux'
import ClusterWorkload from './components/ClusterWorkload'
import { Flex, HStack, Tooltip, Box, Text } from '@chakra-ui/react'
import AccordionComponent from '../../components/AccordionComponent/index.jsx'
import { GeneralLogs, TaskLogs, SecurityLogs, WorkloadLogs } from './components/Logs'
import DBServers from './components/DBServers'
import Proxies from './components/Proxies'
import Apps from './components/Apps/index.jsx'
// Accordion heading with a "?" tooltip explaining log content
function LogHeading({ title, description }) {
  return (
    <HStack spacing={2}>
      <Text as='span'>{title}</Text>
      <Tooltip label={description} placement='right' hasArrow>
        <Box
          as='span'
          cursor='help'
          color='gray.400'
          display='inline-flex'
          alignItems='center'
          justifyContent='center'
          w='14px'
          h='14px'
          borderRadius='full'
          border='1px solid'
          borderColor='gray.400'
          fontSize='10px'
          lineHeight='1'
          fontWeight='bold'>
          ?
        </Box>
      </Tooltip>
    </HStack>
  )
}

function Dashboard({ selectedCluster, user }) {
  const isDesktop = useSelector((state) => state.common.isDesktop)

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

      {selectedCluster && (
        <AccordionComponent
          heading={'Proxies'}
          panelSX={{ overflowX: 'auto', p: 0 }}
          body={<Proxies selectedCluster={selectedCluster} user={user} />}
        />)}

        {selectedCluster && (
        <AccordionComponent
          heading={'Application Servers'}
          panelSX={{ overflowX: 'auto', p: 0 }}
          body={<Apps selectedCluster={selectedCluster} user={user} />}
        />)}

      <AccordionComponent heading={'Cluster Logs'} body={<GeneralLogs />} />
      <AccordionComponent heading={'Job Logs'} body={<TaskLogs />} />
      <AccordionComponent
        heading={
          <LogHeading
            title='Security Logs'
            description='Security compliance findings from audit plugins (SEC0xxx): empty passwords, weak auth, no SSL, missing audit plugin, etc. Also includes API login failures and account lockouts. Does not appear in the main Cluster Logs.'
          />
        }
        body={<SecurityLogs />}
      />
      <AccordionComponent
        heading={
          <LogHeading
            title='Workload Logs'
            description='Performance spike detections based on Graphite time-series analysis: slow query regressions, connection storms, tmp-table storms, full table scan spikes, metadata lock contention, replication lag predictions, error storms. Does not appear in the main Cluster Logs.'
          />
        }
        body={<WorkloadLogs />}
      />
    </Flex>
  )
}

export default Dashboard
