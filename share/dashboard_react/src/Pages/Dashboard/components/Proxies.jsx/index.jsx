import React, { useState } from 'react'
import { useSelector } from 'react-redux'
import ProxyTable from './ProxyTable'
import ProxyGrid from './ProxyGrid'

function Proxies({ selectedCluster, user }) {
  const {
    common: { isDesktop },
    cluster: { clusterProxies }
  } = useSelector((state) => state)

  const [viewType, setViewType] = useState('table')

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
        isMenuOptionsVisible={selectedCluster?.config?.provOrchestrator !== 'onpremise'}
        user={user}
      />
    ) : (
      <ProxyGrid
        proxies={clusterProxies}
        isDesktop={isDesktop}
        clusterName={selectedCluster?.name}
        showTableView={showTableView}
        isMenuOptionsVisible={selectedCluster?.config?.provOrchestrator !== 'onpremise'}
        user={user}
      />
    )
  ) : null
}

export default Proxies
