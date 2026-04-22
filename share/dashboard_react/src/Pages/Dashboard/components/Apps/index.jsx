import React, { useEffect, useState } from 'react'
import { useSelector } from 'react-redux'
import AppTable from './AppTable'
import AppGrid from './AppGrid'

function Apps({ selectedCluster, user }) {
  const isDesktop = useSelector((state) => state.common.isDesktop)
  const clusterApps = useSelector((state) => state.cluster.clusterApps)
  const clusterAppStates = useSelector((state) => state.cluster.clusterAppStates)

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
        orchestrator={selectedCluster?.config?.provOrchestrator}
        user={user}
        states={clusterAppStates}
      />
    ) : (
      <AppGrid
        apps={clusterApps}
        isDesktop={isDesktop}
        clusterName={selectedCluster?.name}
        showTableView={showTableView}
        isMenuOptionsVisible={true}
        orchestrator={selectedCluster?.config?.provOrchestrator}
        user={user}
        states={clusterAppStates}
      />
    )
  ) : null
}

export default Apps
