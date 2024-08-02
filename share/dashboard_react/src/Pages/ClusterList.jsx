import React from 'react'
import { useDispatch, useSelector } from 'react-redux'
import { setCluster } from '../redux/clusterSlice'
import { Box, HStack, Icon, Text, useColorMode, Wrap } from '@chakra-ui/react'
import NotFound from '../components/NotFound'
import { AiOutlineCluster } from 'react-icons/ai'
import { HiCheck, HiExclamation, HiX } from 'react-icons/hi'
import Card from '../components/Card'
import TableType2 from '../components/TableType2'

function ClusterList({ onClick }) {
  const dispatch = useDispatch()
  const { colorMode } = useColorMode()

  const {
    cluster: { clusters, loading }
  } = useSelector((state) => state)

  const styles = {
    linkCard: {
      _hover: {
        color: 'inherit'
      }
    },
    icon: {
      fontSize: '1.5rem'
    },
    green: {
      fill: colorMode === 'light' ? 'green' : 'lightgreen'
    },
    red: { fill: 'red' },

    orange: {
      fill: 'orange'
    }
  }

  return !loading && clusters?.length === 0 ? (
    <NotFound text={'No cluster found!'} />
  ) : (
    <Wrap>
      {clusters?.map((clusterItem) => {
        const dataObject = [
          {
            key: 'Is Monitoring',
            value: (
              <HStack spacing='4'>
                {clusterItem.config.monitoringPause ? (
                  <>
                    <Icon css={{ ...styles.icon, ...styles.red }} as={HiX} />
                    <Text>No</Text>
                  </>
                ) : (
                  <>
                    <Icon css={{ ...styles.icon, ...styles.green }} as={HiCheck} />
                    <Text>Yes</Text>
                  </>
                )}
              </HStack>
            )
          },
          { key: 'Topology', value: clusterItem.topology },
          { key: 'Orchestrator', value: clusterItem.config.provOrchestrator },
          { key: 'Databases', value: clusterItem.dbServers.length },
          { key: 'Proxies', value: clusterItem.proxyServers.length },
          {
            key: 'Is Healthy',
            value: (
              <HStack spacing='4'>
                {clusterItem.isDown ? (
                  <>
                    <Icon sx={{ ...styles.icon, ...styles.red }} as={HiX} />
                    <Text>No</Text>
                  </>
                ) : !clusterItem.isFailable ? (
                  <>
                    <Icon sx={{ ...styles.icon, ...styles.orange }} as={HiExclamation} />
                    <Text>Warning</Text>
                  </>
                ) : (
                  <>
                    <Icon sx={{ ...styles.icon, ...styles.green }} as={HiCheck} />
                    <Text>Yes</Text>
                  </>
                )}
              </HStack>
            )
          },
          {
            key: 'Is Provisioned',
            value: (
              <HStack spacing='4'>
                {clusterItem.isProvision ? (
                  <>
                    <Icon css={[styles.icon, styles.green]} as={HiCheck} />
                    <Text>Yes</Text>
                  </>
                ) : (
                  <>
                    <Icon css={[styles.icon, styles.red]} as={HiX} />
                    <Text>No</Text>
                  </>
                )}
              </HStack>
            )
          },
          { key: 'SLA', value: clusterItem.uptime }
        ]
        return (
          <Box
            sx={styles.linkCard}
            as={'button'}
            mt='8'
            onClick={() => {
              dispatch(setCluster({ data: clusterItem }))
              if (onClick) {
                onClick(clusterItem)
              }
            }}>
            <Card
              width={'400px'}
              header={
                <HStack size={'sm'} sx={styles.heading}>
                  <Icon fontSize='1.5rem' as={AiOutlineCluster} /> <span>{clusterItem.name}</span>
                </HStack>
              }
              body={<TableType2 dataArray={dataObject} />}
            />
          </Box>
        )
      })}
    </Wrap>
  )
}

export default ClusterList
