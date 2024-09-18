import React, { useEffect } from 'react'
import { useDispatch } from 'react-redux'
import { getDatabaseService } from '../../../../redux/clusterSlice'

function MetadataLocks({ clusterName, dbId }) {
  const dispatch = useDispatch()
  useEffect(() => {
    dispatch(getDatabaseService({ clusterName, serviceName: 'meta-data-locks', dbId }))
  }, [])
  return <div>Metadata locks page</div>
}

export default MetadataLocks
