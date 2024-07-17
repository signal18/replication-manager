import React from 'react'
import ClusterDetail from './components/ClusterDetail'
import HADetail from './components/HADetail'
import { useSelector } from 'react-redux'
import ClusterWorkload from './components/ClusterWorkload'
import { Flex } from '@chakra-ui/react'
import AccordionComponent from '../../components/AccordionComponent'
import ClusterTests from './components/ClusterTests'
import Logs from './components/Logs'
import DBServers from './components/DBServers'
import { css } from '@emotion/react'
import Proxies from './components/Proxies.jsx'

function Dashboard({ selectedCluster }) {
  const {
    common: { isDesktop }
  } = useSelector((state) => state)

  const styles = {
    workloadPanel: {
      position: 'relative',
      minHeight: '125px',
      top: '-25px'
    },
    workloadAccordion: {
      // '& .chakra-collapse': {
      //   height: '100px !important',
      //   overflow: 'visible'
      // }
    }
  }

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

      <AccordionComponent
        heading={'Cluster Workload'}
        body={<ClusterWorkload workload={selectedCluster?.workLoad} />}
        sx={styles.workloadAccordion}
        // panelSX={styles.workloadPanel}
      />
      {selectedCluster && (
        <AccordionComponent
          heading={'Database servers'}
          panelSX={{ overflowX: 'auto', p: 0 }}
          body={<DBServers selectedCluster={selectedCluster} />}
        />
      )}

      <AccordionComponent
        heading={'Proxies'}
        panelSX={{ overflowX: 'auto', p: 0 }}
        body={<Proxies selectedCluster={selectedCluster} />}
      />
      {selectedCluster && (
        <AccordionComponent
          heading={'Database servers'}
          panelSX={{ overflowX: 'auto', p: 0 }}
          body={<DBServersTable selectedCluster={selectedCluster} />}
        />
      )}

      <AccordionComponent heading={'Cluster Logs'} body={<Logs logs={selectedCluster?.log?.buffer} />} />
      <AccordionComponent heading={'Job Logs'} body={<Logs logs={selectedCluster?.logTask?.buffer} />} />
    </Flex>
  )
}

export default Dashboard
