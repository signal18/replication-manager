import React, { useEffect } from 'react'
import { useDispatch, useSelector } from 'react-redux'
import { getDatabaseService } from '../../../../redux/clusterSlice'

import CopyObjectText from '../../../../components/CopyObjectText'

function ServiceOpenSvc({ clusterName, dbId }) {
  const dispatch = useDispatch()

  const {
    cluster: {
      database: { serviceOpensvc }
    }
  } = useSelector((state) => state)
  useEffect(() => {
    dispatch(getDatabaseService({ clusterName, serviceName: 'service-opensvc', dbId }))
  }, [])
  return <CopyObjectText text={JSON.stringify(serviceOpensvc)} />
}

export default ServiceOpenSvc
