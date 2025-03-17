import React, { useState } from 'react'
import { useSelector } from 'react-redux'
import AppTable from './AppTable'
import AppGrid from './AppGrid'

function Apps({ selectedCluster, user }) {
  const {
    common: { isDesktop },
    cluster: { clusterApps }
  } = useSelector((state) => state)

  const [viewType, setViewType] = useState('table')

  const showGridView = () => {
    setViewType('grid')
  }
  const showTableView = () => {
    setViewType('table')
  }

  return clusterApps ? (
    viewType === 'table' ? (
      <AppTable
        apps={clusterApps}
        isDesktop={isDesktop}
        clusterName={selectedCluster?.name}
        showGridView={showGridView}
        isMenuOptionsVisible={selectedCluster?.config?.provOrchestrator !== 'onpremise'}
        user={user}
      />
    ) : (
      <AppGrid
        apps={clusterApps}
        isDesktop={isDesktop}
        clusterName={selectedCluster?.name}
        showTableView={showTableView}
        isMenuOptionsVisible={selectedCluster?.config?.provOrchestrator !== 'onpremise'}
        user={user}
      />
    )
  ) : null
}

export default Apps
