import { Box, VStack, Text, Alert, AlertIcon } from '@chakra-ui/react'
import React from 'react'
import { getPreservedVarsCnf, savePreservedVarsCnf } from '../redux/configSlice'
import CnfFileEditor from './CnfFileEditor'
import PropTypes from 'prop-types'

function PreservedVariablesEditor({ clusterName, user, className }) {
  const warningAlert = (
    <>
      This editor manages cluster-level preserved variables that act as a fallback when server-specific 
      preserved variables (01_preserved.cnf) are not defined. These variables have higher priority than 
      MySQL defaults but lower than server-specific configurations.
    </>
  )

  const scopeInfo = (
    <>
      Cluster-wide file path: {clusterName}/preserved_variables.cnf
      <br />
      <strong>Scope:</strong> This configuration applies to all database servers in the cluster as a fallback 
      for preserved variables. Server-specific 01_preserved.cnf files will override these values.
    </>
  )

  const confirmModalContent = (
    <VStack align="start" spacing={2}>
      <Text>
        This will save the <strong>cluster-level</strong> preserved variables configuration and automatically reload it.
      </Text>
      <Text fontSize="sm" color="gray.600">
        <strong>Note:</strong> These variables will be applied to all servers in the cluster unless overridden 
        by server-specific preserved variables (01_preserved.cnf).
      </Text>
      <Text fontWeight="bold" mt={2}>
        Are you sure you want to continue?
      </Text>
    </VStack>
  )

  const infoContent = (
    <VStack align="start" spacing={4}>
      <Box>
        <Text fontWeight="bold" fontSize="lg" mb={2}>
          📋 Cluster-Level Preserved Variables
        </Text>
        <Text fontSize="sm" color="gray.600">
          This file provides cluster-wide fallback values for preserved variables. These variables maintain 
          their values across configuration changes and have higher priority than MySQL defaults.
        </Text>
      </Box>

      <Box>
        <Text fontWeight="bold" mb={2}>
          Configuration Priority (Highest to Lowest):
        </Text>
        <VStack align="start" spacing={2} pl={4}>
          <Text fontSize="sm">
            <strong>1️⃣ Server-Specific Preserved Variables</strong>
            <br />
            <Text as="span" color="gray.600">
              Files: 01_preserved.cnf, 02_delta.cnf, 03_agreed.cnf
              <br />
              Priority: <strong>HIGHEST</strong> - Always wins over all other configurations
            </Text>
          </Text>
          
          <Text fontSize="sm">
            <strong>2️⃣ This File (preserved_variables.cnf)</strong>
            <br />
            <Text as="span" color="gray.600">
              Location: {clusterName}/preserved_variables.cnf
              <br />
              Priority: <strong>HIGH</strong> - Cluster-level preserved variables fallback
            </Text>
          </Text>
          
          <Text fontSize="sm">
            <strong>3️⃣ Legacy Configuration</strong>
            <br />
            <Text as="span" color="gray.600">
              Source: prov-db-config-preserve-vars (deprecated)
              <br />
              Priority: <strong>MEDIUM</strong> - For backward compatibility only
            </Text>
          </Text>
          
          <Text fontSize="sm">
#            <strong>4
 MySQL Defaults</strong>
            <br />
            <Text as="span" color="gray.600">
              Location: {clusterName}/mysql_defaults.cnf
              <br />
              Priority: <strong>LOWEST</strong> - Last resort fallback
            </Text>
          </Text>
        </VStack>
      </Box>

      <Box>
        <Text fontWeight="bold" mb={2}>
          Use Cases:
        </Text>
        <VStack align="start" spacing={1} pl={4}>
          <Text fontSize="sm">• Set cluster-wide defaults for critical variables that should persist</Text>
          <Text fontSize="sm">• Override deprecated prov-db-config-preserve-vars settings</Text>
          <Text fontSize="sm">• Provide fallback values when server-specific preserved variables are not set</Text>
          <Text fontSize="sm">• Centrally manage preserved variables for all servers in: <strong>{clusterName}</strong></Text>
        </VStack>
      </Box>

      <Alert status="info" borderRadius="md">
        <AlertIcon />
        <Box fontSize="sm">
          <strong>💡 Migration:</strong> This mechanism replaces the deprecated prov-db-config-preserve-vars 
          configuration option. Consider migrating your preserved variables to this file-based approach.
        </Box>
      </Alert>
    </VStack>
  )

  return (
    <CnfFileEditor
      clusterName={clusterName}
      user={user}
      className={className}
      fileType="Preserved Variables"
      fileName="preserved_variables.cnf"
      loadAction={getPreservedVarsCnf}
      saveAction={savePreservedVarsCnf}
      placeholder="Preserved variables configuration content..."
      warningAlert={warningAlert}
      scopeInfo={scopeInfo}
      confirmModalContent={confirmModalContent}
      infoContent={infoContent}
    />
  )
}

PreservedVariablesEditor.propTypes = {
  clusterName: PropTypes.string.isRequired,
  user: PropTypes.object,
  className: PropTypes.string
}

export default PreservedVariablesEditor
