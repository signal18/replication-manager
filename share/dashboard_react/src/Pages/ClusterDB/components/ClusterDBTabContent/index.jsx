import React, { useEffect, useState } from 'react'

function ClusterDBTabContent({ tab }) {
  const [currentTab, setCurrentTab] = useState('')

  useEffect(() => {
    setCurrentTab(tab)
  }, [tab])
  return currentTab === 'processlist' ? (
    <div>process list</div>
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
