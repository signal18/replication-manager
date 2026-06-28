import { HStack, VStack, Text, Badge } from '@chakra-ui/react'
import PropTypes from 'prop-types'
import { useTheme } from '../../../../ThemeProvider'

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

function getRouteBadgeProps(theme, kind) {
  if (theme !== 'dark') {
    return { colorScheme: kind }
  }

  if (kind === 'green') {
    return {
      bg: 'rgba(15, 23, 42, 0.36)',
      color: '#d9fbe8',
      border: '1px solid rgba(104, 211, 145, 0.45)',
    }
  }

  if (kind === 'yellow') {
    return {
      bg: 'rgba(15, 23, 42, 0.36)',
      color: '#fef3c7',
      border: '1px solid rgba(246, 173, 85, 0.45)',
    }
  }

  return {
    bg: 'rgba(15, 23, 42, 0.36)',
    color: '#fee2e2',
    border: '1px solid rgba(252, 129, 129, 0.48)',
  }
}

function RouteStateBadges({ up, warn, down, theme, compact }) {
  return (
    <HStack spacing={1} flexWrap="nowrap" align="center" justify={compact ? 'center' : 'flex-start'} width={compact ? '100%' : 'auto'}>
      {up > 0 && <Badge fontSize="2xs" px={1.5} {...getRouteBadgeProps(theme, 'green')}>{up} up</Badge>}
      {warn > 0 && <Badge fontSize="2xs" px={1.5} {...getRouteBadgeProps(theme, 'yellow')}>{warn} warn</Badge>}
      {down > 0 && <Badge fontSize="2xs" px={1.5} {...getRouteBadgeProps(theme, 'red')}>{down} down</Badge>}
    </HStack>
  )
}

function RouteSummary({ routeStatuses, configuredRouteCount = null, showIdentity = false, compact = false }) {
  const { theme } = useTheme()

  // Caller explicitly confirmed no routes are configured — no need to wait for monitoring.
  if (configuredRouteCount === 0) {
    return <Text fontSize="xs" color="gray.400">No routes</Text>;
  }

  // Runtime status hasn't arrived yet; routes may or may not be configured.
  if (routeStatuses == null) {
    return <Text fontSize="xs" color="gray.400">—</Text>;
  }

  const { total, up, warn, down, primaryRoute } = routeStatusSummary(routeStatuses);
  const primaryLabel = primaryRouteLabel(primaryRoute);

  if (total === 0) {
    // Configured routes exist but runtime status is empty → still pending.
    // Without config knowledge, fall back to "No routes".
    return configuredRouteCount > 0
      ? <Text fontSize="xs" color="gray.400">—</Text>
      : <Text fontSize="xs" color="gray.400">No routes</Text>;
  }

  const summary = (
    <VStack spacing={0.5} align="start" maxW="100%">
      {(showIdentity || compact) && primaryLabel && (
        <Text fontSize="xs" fontWeight="medium" color={theme === 'dark' ? 'gray.200' : 'gray.700'} whiteSpace="nowrap" overflow="hidden" textOverflow="ellipsis" maxW="180px">
          {primaryLabel}
        </Text>
      )}
      <RouteStateBadges up={up} warn={warn} down={down} theme={theme} />
    </VStack>
  );

  if (compact) {
    return <RouteStateBadges up={up} warn={warn} down={down} theme={theme} compact />;
  }

  return summary;
}

export default RouteSummary

RouteSummary.propTypes = {
  routeStatuses: PropTypes.array,
  configuredRouteCount: PropTypes.number,
  showIdentity: PropTypes.bool,
  compact: PropTypes.bool,
}

RouteStateBadges.propTypes = {
  up: PropTypes.number.isRequired,
  warn: PropTypes.number.isRequired,
  down: PropTypes.number.isRequired,
  theme: PropTypes.string.isRequired,
  compact: PropTypes.bool,
}
