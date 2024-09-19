import React, { useEffect } from 'react'
import { useDispatch } from 'react-redux'
import { getDatabaseService } from '../../../../redux/clusterSlice'

function ServiceOpenSvc({ clusterName, dbId }) {
  const dispatch = useDispatch()
  useEffect(() => {
    dispatch(getDatabaseService({ clusterName, serviceName: 'service-opensvc', dbId }))
  }, [])
  return <div>ServiceOpenSvc page</div>
}

export default ServiceOpenSvc
