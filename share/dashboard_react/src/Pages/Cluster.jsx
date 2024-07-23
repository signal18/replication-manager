import React from 'react'
import { useSelector } from 'react-redux'
import Dashboard from './Dashboard'
import Settings from './Settings'

function Cluster({ tab }) {
  const {
    cluster: { clusterData }
  } = useSelector((state) => state)

  return tab === 'dashboard' ? <Dashboard selectedCluster={clusterData} /> : tab === 'settings' ? <Settings /> : null
}

export default Cluster
