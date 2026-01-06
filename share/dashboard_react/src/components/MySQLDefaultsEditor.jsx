import { Box, VStack, Text, Alert, AlertIcon } from '@chakra-ui/react'
import { getMySQLDefaultsCnf, saveMySQLDefaultsCnf } from '../redux/configSlice'
import CnfFileEditor from './CnfFileEditor'
import PropTypes from 'prop-types'

function MySQLDefaultsEditor({ clusterName, user, className }) {
  const warningAlert = (
    <>
      Remember that this editor manages the cluster-wide fallback configuration, 
      not individual server configurations. Use this only for setting default values across the entire cluster.
    </>
  )

  const scopeInfo = (
    <>
      Cluster-wide file path: {clusterName}/mysql_defaults.cnf
      <br />
      <strong>Scope:</strong> This configuration applies to all database servers in the cluster as a last-resort fallback only.
    </>
  )

  const confirmModalContent = (
    <VStack align="start" spacing={2}>
      <Text>
        This will save the <strong>cluster-wide</strong> MySQL defaults configuration and automatically reload it.
      </Text>
      <Text fontSize="sm" color="gray.600">
        <strong>Reminder:</strong> This is a fallback configuration file. Primary server configurations are managed separately through Configurator settings.
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
          ⚠️ IMPORTANT: This is a Fallback Configuration File
        </Text>
        <Text fontSize="sm" color="gray.600">
          This file is used <strong>only as a last resort</strong> and does NOT control primary server configurations.
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
            <strong>2️⃣ Configurator Settings</strong>
            <br />
            <Text as="span" color="gray.600">
              Source: Tags, templates, dynamic configs
              <br />
              Priority: <strong>HIGH</strong> - Primary configuration source for all servers
            </Text>
          </Text>
          
          <Text fontSize="sm">
            <strong>3️⃣ Runtime/Deployed Values</strong>
            <br />
            <Text as="span" color="gray.600">
              Source: Live server values from MySQL
              <br />
              Priority: <strong>MEDIUM</strong> - Current operational values
            </Text>
          </Text>
          
          <Text fontSize="sm">
            <strong>4️⃣ This File (mysql_defaults.cnf)</strong>
            <br />
            <Text as="span" color="gray.600">
              Location: {clusterName}/mysql_defaults.cnf
              <br />
              Priority: <strong>LOWEST</strong> - Only used when no other values exist
            </Text>
          </Text>
        </VStack>
      </Box>

      <Box>
        <Text fontWeight="bold" mb={2}>
          When This File is Used:
        </Text>
        <VStack align="start" spacing={1} pl={4}>
          <Text fontSize="sm">• Only when a database server is in <strong>FAILED</strong> state</Text>
          <Text fontSize="sm">• Only when no runtime, deployed, or preserved values exist for a variable</Text>
          <Text fontSize="sm">• Applies to all servers in cluster: <strong>{clusterName}</strong></Text>
        </VStack>
      </Box>

      <Alert status="info" borderRadius="md">
        <AlertIcon />
        <Box fontSize="sm">
          <strong>💡 Best Practice:</strong> For normal operations, use Configurator settings and server-specific preserved variables instead of this fallback file.
        </Box>
      </Alert>
    </VStack>
  )

  return (
    <CnfFileEditor
      clusterName={clusterName}
      user={user}
      className={className}
      fileType="MySQL Defaults"
      fileName="mysql_defaults.cnf"
      loadAction={getMySQLDefaultsCnf}
      saveAction={saveMySQLDefaultsCnf}
      placeholder="MySQL defaults configuration content..."
      warningAlert={warningAlert}
      scopeInfo={scopeInfo}
      confirmModalContent={confirmModalContent}
      infoContent={infoContent}
    />
  )
}

MySQLDefaultsEditor.propTypes = {
  clusterName: PropTypes.string.isRequired,
  user: PropTypes.object,
  className: PropTypes.string
}

export default MySQLDefaultsEditor
