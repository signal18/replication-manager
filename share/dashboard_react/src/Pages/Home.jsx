import React from 'react'
import { Box } from '@chakra-ui/react'

import PageContainer from './PageContainer'
import TabItems from '../components/TabItems'
import ClusterList from './ClusterList'
import { useSelector } from 'react-redux'

function Home() {
  const {
    common: { isDesktop }
  } = useSelector((state) => state)
  return (
    <PageContainer>
      <Box m={isDesktop ? '6' : '3'}>
        <TabItems options={['Clusters']} tabContents={[<ClusterList />]} />
      </Box>
    </PageContainer>
  )
}

export default Home
