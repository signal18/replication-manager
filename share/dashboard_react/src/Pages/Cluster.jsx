import React, { useEffect, useState } from 'react'
import { useSelector } from 'react-redux'
import Dashboard from './Dashboard'
import Settings from './Settings'

function Cluster({ tab }) {
  const [user, setUser] = useState(null)
  const {
    cluster: { clusterData }
  } = useSelector((state) => state)

  useEffect(() => {
    if (clusterData?.apiUsers) {
      const loggedUser = localStorage.getItem('username')
      if (loggedUser && clusterData?.apiUsers[loggedUser]) {
        const apiUser = clusterData.apiUsers[loggedUser]
        setUser(apiUser)
      }
    }
  }, [clusterData?.apiUsers])

  return tab === 'dashboard' ? (
    <Dashboard selectedCluster={clusterData} user={user} />
  ) : tab === 'settings' ? (
    <Settings selectedCluster={clusterData} user={user} />
  ) : null
}

export default Cluster
