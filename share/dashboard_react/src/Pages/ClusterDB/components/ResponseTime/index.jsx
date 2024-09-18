import React, { useEffect } from 'react'
import { useDispatch } from 'react-redux'
import { getDatabaseService } from '../../../../redux/clusterSlice'

function ResponseTime({ clusterName, dbId }) {
  const dispatch = useDispatch()
  useEffect(() => {
    dispatch(getDatabaseService({ clusterName, serviceName: 'query-response-time', dbId }))
  }, [])
  return <div>Response time page</div>
}

export default ResponseTime
