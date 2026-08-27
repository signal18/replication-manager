import React, { useEffect } from 'react'
import { useDispatch, useSelector } from 'react-redux'
import { Box, Text, VStack } from '@chakra-ui/react'
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

// Kubernetes has no single "service config" object the way OpenSVC does, so
// service/{orchestrator} (buildDatabaseServiceConfigResponse,
// server/api_database.go) returns live Deployment/Service/PVC/Pod manifest
// YAML sections as JSON instead of one raw text blob (#1497 gap 6).
const K8S_MANIFEST_SECTIONS = [
  { key: 'deployment', label: 'Deployment' },
  { key: 'service', label: 'Service' },
  { key: 'pvc', label: 'PersistentVolumeClaim' },
  { key: 'pods', label: 'Pods' }
]

function ServiceOpenSvc({ clusterName, type, id, user, orchestrator }) {
  const dispatch = useDispatch()
  const serviceConfig = useOpenSvcSelector(type)
  // type="app" always uses the (unrelated, OpenSVC-only) app-level route --
  // only the db route is orchestrator-dynamic.
  const serviceName = type === 'db' && orchestrator ? `service/${orchestrator}` : 'service-opensvc'

  useEffect(() => {
    if (type === "db") {
      if (orchestrator) {
        dispatch(getDatabaseService({ clusterName, serviceName, dbId: id }))
      }
    } else if (type === "app") {
      dispatch(getAppService({ clusterName, serviceName, appId: id }))
    }
  }, [type, orchestrator])

  if (type === 'db' && orchestrator === 'kube' && serviceConfig && typeof serviceConfig === 'object') {
    return (
      <VStack align="stretch" spacing={6}>
        {K8S_MANIFEST_SECTIONS.map(({ key, label }) => (
          <Box key={key}>
            <Text fontWeight="bold" mb={2}>{label}</Text>
            <CopyObjectText text={serviceConfig[key] || ''} showPrettyJsonCheckbox={false} />
          </Box>
        ))}
      </VStack>
    )
  }

  return <CopyObjectText text={JSON.stringify(serviceConfig)} />
}

export default ServiceOpenSvc
