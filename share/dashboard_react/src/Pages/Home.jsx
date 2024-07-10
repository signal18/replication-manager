import React from 'react'
import { Box } from '@chakra-ui/react'

import PageContainer from './PageContainer'
import TabItems from '../components/TabItems'
import ClusterList from './ClusterList'

function Home() {
  return (
    <PageContainer>
      <Box m='6'>
        <TabItems options={['Clusters']} tabContents={[<ClusterList />]} />
      </Box>
    </PageContainer>
  )
}

export default Home
