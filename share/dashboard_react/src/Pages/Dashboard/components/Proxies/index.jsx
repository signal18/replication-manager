import { useState } from 'react'
import { useSelector } from 'react-redux'
import ProxyTable from './ProxyTable'
import ProxyGrid from './ProxyGrid'

function Proxies({ selectedCluster, user }) {
  const isDesktop = useSelector((state) => state.common.isDesktop)
  const clusterProxies = useSelector((state) => state.cluster.clusterProxies)

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
        showTerminal={selectedCluster?.config?.terminalSessionEnabled}
        topoStaging={selectedCluster?.config?.topologyStaging}
        orchestrator={selectedCluster?.config?.provOrchestrator}
        user={user}
      />
    ) : (
      <ProxyGrid
        proxies={clusterProxies}
        isDesktop={isDesktop}
        clusterName={selectedCluster?.name}
        showTableView={showTableView}
        isMenuOptionsVisible={selectedCluster?.config?.provOrchestrator !== 'onpremise'}
        showTerminal={selectedCluster?.config?.terminalSessionEnabled}
        topoStaging={selectedCluster?.config?.topologyStaging}
        orchestrator={selectedCluster?.config?.provOrchestrator}
        user={user}
      />
    )
  ) : null
}

export default Proxies
