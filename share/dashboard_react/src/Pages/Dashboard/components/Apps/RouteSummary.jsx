import { HStack, VStack, Text, Badge } from '@chakra-ui/react'

function routeStatusSummary(routeStatuses) {
  if (!routeStatuses?.length) return { total: 0, up: 0, warn: 0, down: 0, primaryRoute: null };
  let up = 0, warn = 0, down = 0, primaryRoute = null;
  for (const rs of routeStatuses) {
    if (rs.primary && !primaryRoute) primaryRoute = rs;
    if (rs.status === 'AppRunning') up++;
    else if (rs.status === 'AppWarning') warn++;
    else down++;
  }
  return { total: routeStatuses.length, up, warn, down, primaryRoute };
}

function primaryRouteLabel(route) {
  if (!route) return '';
  const mode = route.mode || (route.protocol === 'tcp' ? 'port' : 'host');
  if (mode === 'port') {
    const cname = route.cname || '';
    const sourcePort = route.sourcePort || '';
    if (cname && sourcePort) return `${cname}:${sourcePort}`;
    return cname || (sourcePort ? `:${sourcePort}` : 'port route');
  }
  return route.cname || '';
}

function RouteSummary({ routeStatuses, configuredRouteCount = null, showIdentity = false }) {
  // Caller explicitly confirmed no routes are configured — no need to wait for monitoring.
  if (configuredRouteCount === 0) {
    return <Text fontSize="xs" color="gray.400">No routes</Text>;
  }

  // Runtime status hasn't arrived yet; routes may or may not be configured.
  if (routeStatuses == null) {
    return <Text fontSize="xs" color="gray.400">—</Text>;
  }

  const { total, up, warn, down, primaryRoute } = routeStatusSummary(routeStatuses);

  if (total === 0) {
    // Configured routes exist but runtime status is empty → still pending.
    // Without config knowledge, fall back to "No routes".
    return configuredRouteCount > 0
      ? <Text fontSize="xs" color="gray.400">—</Text>
      : <Text fontSize="xs" color="gray.400">No routes</Text>;
  }

  return (
    <VStack spacing={0} align="start">
      <HStack spacing={1} flexWrap="nowrap">
        <Text fontSize="xs" color="gray.600" whiteSpace="nowrap">{total} {total === 1 ? 'route' : 'routes'}</Text>
        {up > 0 && <Badge colorScheme="green" fontSize="2xs" px={1}>{up} up</Badge>}
        {warn > 0 && <Badge colorScheme="yellow" fontSize="2xs" px={1}>{warn} warn</Badge>}
        {down > 0 && <Badge colorScheme="red" fontSize="2xs" px={1}>{down} down</Badge>}
      </HStack>
      {showIdentity && primaryRoute && (
        <Text fontSize="2xs" color="gray.400" whiteSpace="nowrap" overflow="hidden" textOverflow="ellipsis" maxW="180px">
          {primaryRouteLabel(primaryRoute)}
        </Text>
      )}
    </VStack>
  );
}

export default RouteSummary
