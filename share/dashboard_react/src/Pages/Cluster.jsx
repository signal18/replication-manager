import React, { lazy, useEffect, useState } from 'react'
import { useSelector } from 'react-redux'
import Dashboard from './Dashboard'
import Agents from './Agents'
import Backups from './Backups'
const Configs = lazy(() => import('./Configs'))
const Settings = lazy(() => import('./Settings'))
const Graphs = lazy(() => import('./Graphs'))

function Cluster({ tab }) {
  const [user, setUser] = useState(null)
  const [currentTab, setCurrentTab] = useState('')
  const {
    cluster: { clusterData }
  } = useSelector((state) => state)

  useEffect(() => {
    setCurrentTab(tab)
  }, [tab])

  useEffect(() => {
    if (clusterData?.apiUsers) {
      const loggedUser = localStorage.getItem('username')
      if (loggedUser && clusterData?.apiUsers[loggedUser]) {
        const apiUser = clusterData.apiUsers[loggedUser]
        setUser(apiUser)
      }
    }
  }, [clusterData?.apiUsers])

  return currentTab === 'dashboard' ? (
    <Dashboard selectedCluster={clusterData} user={user} />
  ) : currentTab === 'settings' ? (
    <Settings selectedCluster={clusterData} user={user} />
  ) : currentTab === 'agents' ? (
    <Agents selectedCluster={clusterData} user={user} />
  ) : currentTab === 'configs' ? (
    <Configs selectedCluster={clusterData} user={user} />
  ) : currentTab === 'backups' ? (
    <Backups selectedCluster={clusterData} />
  ) : currentTab === 'graphs' ? (
    <Graphs />
  ) : null
}

export default Cluster
