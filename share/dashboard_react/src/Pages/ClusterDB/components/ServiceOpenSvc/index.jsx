import React, { useEffect } from 'react'
import { useDispatch, useSelector } from 'react-redux'
import { getAppService, getDatabaseService } from '../../../../redux/clusterSlice'

import CopyObjectText from '../../../../components/CopyObjectText'

const useOpenSvcSelector = (type) => {
  return useSelector((state) => {
    switch (type) {
      case "db":
        return state.cluster.database.serviceOpensvc;
      case "app":
        return state.cluster.app.serviceOpensvc;
      default:
        return null;
    }
  });
};


function ServiceOpenSvc({ clusterName, type, id, user }) {
  const dispatch = useDispatch()
  const serviceOpensvc = useOpenSvcSelector(type)
  const serviceName = 'service-opensvc'

  useEffect(() => {
    if (type === "db") {
      dispatch(getDatabaseService({ clusterName, serviceName, dbId: id }))
    } else if (type === "app") {
      dispatch(getAppService({ clusterName, serviceName, appId: id }))
    }
  }, [type])
  return <CopyObjectText text={JSON.stringify(serviceOpensvc)} />
}

export default ServiceOpenSvc
