import { useState } from 'react'
import { useSelector } from 'react-redux'
import AppTable from './AppTable'
import AppGrid from './AppGrid'

function Apps({ selectedCluster, user }) {
  const isDesktop = useSelector((state) => state.common.isDesktop)
  const clusterApps = useSelector((state) => state.cluster.clusterApps)
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
        orchestrator={selectedCluster?.config?.provOrchestrator}
        user={user}
      />
    ) : (
      <AppGrid
        apps={clusterApps}
        isDesktop={isDesktop}
        clusterName={selectedCluster?.name}
        showTableView={showTableView}
        orchestrator={selectedCluster?.config?.provOrchestrator}
        user={user}
      />
    )
  ) : null
}

export default Apps
