import React, { useEffect, useState } from 'react'
import { useDispatch, useSelector } from 'react-redux'
import { getClusterPeers } from '../../redux/globalClustersSlice'
import { Box, Flex, HStack, Text } from '@chakra-ui/react'
import NotFound from '../../components/NotFound'
import { AiOutlineCluster } from 'react-icons/ai'
import Card from '../../components/Card'
import TableType2 from '../../components/TableType2'
import styles from './styles.module.scss'
import CheckOrCrossIcon from '../../components/Icons/CheckOrCrossIcon'
import CustomIcon from '../../components/Icons/CustomIcon'
import TagPill from '../../components/TagPill'

function PeerClusterList({}) {
  const dispatch = useDispatch()

  const {
    globalClusters: { loading, clusterPeers }
  } = useSelector((state) => state)

  useEffect(() => {
    dispatch(getClusterPeers({}))
  }, [])

  return !loading && clusterPeers?.length === 0 ? (
    <NotFound text={'No peer cluster found!'} />
  ) : (
    <Flex className={styles.clusterList}>
      {clusterPeers?.map((clusterItem) => {
        const headerText = `${clusterItem['cluster-name']}@${clusterItem['cloud18-domain']}-${clusterItem['cloud18-sub-domain']}-${clusterItem['cloud18-sub-domain-zone']}`

        const dataObject = [
          { key: 'Domain', value: clusterItem['cloud18-domain'] },
          { key: 'Platfom Desciption', value: clusterItem['cloud18-platfom-desciption'] },

          {
            key: 'Share',
            value: (
              <HStack spacing='4'>
                {clusterItem['cloud18-share'] ? (
                  <>
                    <CheckOrCrossIcon isValid={true} />
                    <Text>Yes</Text>
                  </>
                ) : (
                  <>
                    <CheckOrCrossIcon isValid={false} />
                    <Text>No</Text>
                  </>
                )}
              </HStack>
            )
          }
        ]

        return (
          <Box key={clusterItem['cluster-name']} className={styles.cardWrapper}>
            <Card
              className={styles.card}
              width={'400px'}
              header={
                <HStack
                  as='button'
                  className={styles.btnHeading}
                  onClick={() => {
                    fetch(`https://${clusterItem['api-plublic-url']}/api/login`, {
                      method: 'POST'
                    })
                      .then((res) => res.json())
                      .then((data) => console.log('data::', data))
                  }}>
                  <CustomIcon icon={AiOutlineCluster} />
                  <span className={styles.cardHeaderText}>{headerText}</span>

                  <TagPill text='Cloud18' colorScheme='blue' />
                </HStack>
              }
              body={
                <TableType2
                  dataArray={dataObject}
                  className={styles.table}
                  labelClassName={styles.rowLabel}
                  valueClassName={styles.rowValue}
                />
              }
            />
          </Box>
        )
      })}
    </Flex>
  )
}

export default PeerClusterList
