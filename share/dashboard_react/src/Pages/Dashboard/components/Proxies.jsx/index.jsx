import { VStack } from '@chakra-ui/react'
import React from 'react'
import { useSelector } from 'react-redux'
import ProxyInfoTable from './ProxyInfoTable'
import BackendReadTable from './BackendReadTable'

function Proxies() {
  const {
    common: { isDesktop },
    cluster: { clusterProxies }
  } = useSelector((state) => state)

  return clusterProxies ? (
    <VStack>
      <ProxyInfoTable proxies={clusterProxies} isDesktop={isDesktop} />
      {/* <BackendReadTable backendReadData={clusterProxies?.backendsRead} /> */}
    </VStack>
  ) : null
}

export default Proxies
