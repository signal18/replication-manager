import { VStack } from '@chakra-ui/react'
import React, { useEffect, useState } from 'react'
import { useSelector } from 'react-redux'
import ProxyTable from './ProxyTable'
import ProxyGrid from './ProxyGrid'

function Proxies({ selectedCluster }) {
  const {
    common: { isDesktop },
    cluster: { clusterProxies }
  } = useSelector((state) => state)

  const [viewType, setViewType] = useState('table')
  const [user, setUser] = useState(null)

  useEffect(() => {
    const loggedUser = localStorage.getItem('username')
    if (loggedUser && selectedCluster?.apiUsers[loggedUser]) {
      const apiUser = selectedCluster.apiUsers[loggedUser]
      setUser(apiUser)
    }
  }, [selectedCluster])

  const showGridView = () => {
    setViewType('grid')
  }
  const showTableView = () => {
    setViewType('table')
  }

  return clusterProxies ? (
    viewType === 'table' ? (
      <ProxyTable
        proxies={clusterProxies}
        isDesktop={isDesktop}
        clusterName={selectedCluster?.name}
        showGridView={showGridView}
      />
    ) : (
      <ProxyGrid
        proxies={clusterProxies}
        isDesktop={isDesktop}
        clusterName={selectedCluster?.name}
        showTableView={showTableView}
        user={user}
      />
    )
  ) : null
}

export default Proxies
