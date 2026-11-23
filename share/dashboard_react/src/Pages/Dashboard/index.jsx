import React from 'react'
import ClusterDetail from './components/ClusterDetail'
import HADetail from './components/HADetail/index.jsx'
import { useSelector } from 'react-redux'
import ClusterWorkload from './components/ClusterWorkload'
import { Flex } from '@chakra-ui/react'
import AccordionComponent from '../../components/AccordionComponent/index.jsx'
import ClusterLogsTabs from './components/ClusterLogsTabs'
import DBServers from './components/DBServers'
import Proxies from './components/Proxies'
import Apps from './components/Apps/index.jsx'
import RMIconButton from '../../components/RMIconButton'
import TableType2 from '../../components/TableType2'
import styles from './styles.module.scss'
import { HiCog } from 'react-icons/hi'
const toolLabels = {
  client: 'Database client',
  'client-dump': 'Dump client',
  'client-binlog': 'Binlog client',
  mydumper: 'mydumper',
  sysbench: 'sysbench',
  restic: 'restic'
}


function Dashboard({ selectedCluster, user, openSettings = {} }) {
  const isDesktop = useSelector((state) => state.common.isDesktop)

  const gearButton = (onClick, tooltip) =>
    onClick ? (
      <RMIconButton icon={HiCog} tooltip={tooltip} onClick={onClick} size='xs' variant='ghost' />
    ) : null

  return (
    <Flex direction='column' gap='8px'>
      <Flex gap='24px'>
        {selectedCluster && (
          <Flex w='100%' gap='24px' direction={isDesktop ? 'row' : 'column'}>
            <ClusterDetail selectedCluster={selectedCluster} user={user} onOpenSettings={openSettings.topology} />
            <HADetail selectedCluster={selectedCluster} user={user} onOpenSettings={openSettings.failover} />
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
          headerActions={gearButton(openSettings.topology, 'Topology Settings')}
          body={<DBServers selectedCluster={selectedCluster} user={user} />}
        />
      )}

      {selectedCluster && (
        <AccordionComponent
          heading={'Proxies'}
          panelSX={{ overflowX: 'auto', p: 0 }}
          headerActions={gearButton(openSettings.proxies, 'Proxy Settings')}
          body={<Proxies selectedCluster={selectedCluster} user={user} />}
        />)}

        {selectedCluster && (
        <AccordionComponent
          heading={'Application Servers'}
          panelSX={{ overflowX: 'auto', p: 0 }}
          body={<Apps selectedCluster={selectedCluster} user={user} />}
        />)}

      {/* every cluster log in one place, one tab per domain, HA first */}
      <ClusterLogsTabs openSettings={openSettings} />
    </Flex>
  )
}

export default Dashboard
