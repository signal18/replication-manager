import { Flex, Text } from '@chakra-ui/react'
import SectionFrame from '../../components/SectionFrame'
import SecretKvEntries from '../Settings/SecretKvEntries'

// The instance's server credential stores, a global tab of its own: the instance's sysops and
// extsysops (the server credentials grant on every cluster) and the local admin, whatever the
// sign-in (Cloud18 or local). The API checks the same grant (serverCredentialsACL); the rest of the
// global settings stay the local admin's (Setup).
function ServerCredentials() {
  return (
    <Flex direction='column' gap='3' p='3'>
      <SectionFrame title='Server Credential Stores'>
        <Text fontSize='sm' mb='3'>
          The stores that keep every version of this instance's server secrets (the API users' list
          api-credentials, the SMTP and GitLab passwords...): a vault (path-prefix/system/repman/&lt;instance
          name&gt;), the OpenSVC secret system/sec/repman or a Kubernetes Secret repman-system, the last two
          through the connection of a cluster on that orchestrator. They are never copied to a cluster's stores.
          Only this instance's sysops and extsysops see and change them. A store behind shows a state here and on
          every cluster.
        </Text>
        <SecretKvEntries level='server' />
      </SectionFrame>
    </Flex>
  )
}

export default ServerCredentials

// holdsServerCredentials: the logged user may see the server stores: the local admin, or the
// server credentials grant on every cluster it has grants on (the instance's sysops and extsysops).
export function holdsServerCredentials(loggedUser) {
  if (loggedUser?.User === 'admin') return true
  const grants = loggedUser?.grants || {}
  const clusters = Object.keys(grants)
  return clusters.length > 0 && clusters.every((c) => grants[c]?.['cluster-server-credentials'])
}
