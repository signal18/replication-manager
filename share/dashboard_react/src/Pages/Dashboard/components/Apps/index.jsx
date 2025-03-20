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
        isMenuOptionsVisible={true}
        user={user}
      />
    ) : (
      <AppGrid
        apps={clusterApps}
        isDesktop={isDesktop}
        clusterName={selectedCluster?.name}
        showTableView={showTableView}
        isMenuOptionsVisible={true}
        user={user}
      />
    )
  ) : null
}

export default Apps
