import React, { useEffect, useState } from 'react'
import ProcessList from '../ProcessList'
import { useSelector } from 'react-redux'

function ClusterDBTabContent({ tab, dbId, clusterName }) {
  const [currentTab, setCurrentTab] = useState('')
  const [selectedDBServer, setSelectedDBServer] = useState(null)
  const [user, setUser] = useState(null)

  const {
    cluster: { clusterServers, clusterData }
  } = useSelector((state) => state)

  useEffect(() => {
    setCurrentTab(tab)
  }, [tab])

  useEffect(() => {
    if (clusterServers?.length > 0 && dbId) {
      const server = clusterServers.find((x) => x.id === dbId)
      setSelectedDBServer(server)
    }
    if (clusterData?.apiUsers) {
      const loggedUser = localStorage.getItem('username')
      if (loggedUser && clusterData?.apiUsers[loggedUser]) {
        const apiUser = clusterData.apiUsers[loggedUser]
        setUser(apiUser)
      }
    }
  }, [dbId])
  return currentTab === 'processlist' ? (
    <ProcessList selectedDBServer={selectedDBServer} user={user} clusterName={clusterName} />
  ) : currentTab === 'slowqueries' ? (
    <div>slow queries</div>
  ) : currentTab === 'digestqueries' ? (
    <div>digest queries</div>
  ) : currentTab === 'errors' ? (
    <div>errors</div>
  ) : currentTab === 'tables' ? (
    <div>tables</div>
  ) : currentTab === 'status' ? (
    <div>status</div>
  ) : null
}

export default ClusterDBTabContent
