import React from 'react'
import Card from '../../../components/Card'
import { Box, Grid, GridItem, Text, Wrap } from '@chakra-ui/react'
import TagPill from '../../../components/TagPill'
import { useSelector } from 'react-redux'

function ClusterDetail({ selectedCluster }) {
  const {
    common: { theme }
  } = useSelector((state) => state)

  const dataObject = [
    { key: 'Name', value: selectedCluster.name },
    { key: 'Orchestrator', value: selectedCluster.config.provOrchestrator },
    {
      key: 'Status',
      value: (
        <Wrap>
          {
            <>
              {selectedCluster.config.testInjectTraffic && <TagPill type='success' text='PrxTraffic' />}
              {selectedCluster.isProvision ? (
                <TagPill type='success' text='IsProvision' />
              ) : (
                <TagPill type='warning' text='NeedProvision' />
              )}
              {selectedCluster.isNeedDatabasesRollingRestart && <TagPill type='warning' text='NeedRollingRestart' />}
              {selectedCluster.isNeedDatabasesRollingReprov && <TagPill type='warning' text='NeedRollingReprov' />}
              {selectedCluster.isNeedDatabasesRestart && <TagPill type='warning' text='NeedDabaseRestart' />}
              {selectedCluster.isNeedDatabasesReprov && <TagPill type='warning' text='NeedDatabaseReprov' />}
              {selectedCluster.isNeedProxiesRestart && <TagPill type='warning' text='NeedProxyRestart' />}
              {selectedCluster.isNeedProxiesReprov && <TagPill type='warning' text='NeedProxyReprov' />}
              {selectedCluster.isNotMonitoring && <TagPill type='warning' text='UnMonitored' />}
              {selectedCluster.isCapturing && (
                <TagPill
                  type='warning'
                  text='Capturing
              
              '
                />
              )}
            </>
          }
        </Wrap>
      )
    }
  ]

  return (
    <Card
      width={'50%'}
      header={
        <>
          <Text>Cluster</Text>
          {selectedCluster?.activePassiveStatus === 'A' ? (
            <TagPill type='success' text={'Active'} />
          ) : selectedCluster?.activePassiveStatus === 'S' ? (
            <TagPill type='warning' text={'Standby'} />
          ) : null}
        </>
      }
      body={
        <Grid templateColumns='150px auto' gap={2} p={4}>
          {dataObject.map((item, index) => (
            <React.Fragment key={index}>
              <GridItem>
                <Box p={2} borderRadius='md'>
                  {item.key}
                </Box>
              </GridItem>
              <GridItem>
                <Box bg={theme === 'light' ? 'gray.50' : 'gray.700'} p={2} borderRadius='md'>
                  {item.value}
                </Box>
              </GridItem>
            </React.Fragment>
          ))}
        </Grid>
      }
      showDashboardOptions={true}></Card>
  )
}

export default ClusterDetail
