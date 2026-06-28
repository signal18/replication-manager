import {
  VStack, HStack, Text, Heading, Input, Select, Flex, Tooltip, Box,
  Table, Thead, Tbody, Tr, Th, Td, Badge, Divider, SimpleGrid,
} from '@chakra-ui/react'
import React, { useMemo, useState } from 'react'
import { HiTrash, HiInformationCircle, HiChevronDown, HiChevronRight } from 'react-icons/hi'
import { TbEdit } from 'react-icons/tb'
import PropTypes from 'prop-types'
import RMIconButton from '../../../../components/RMIconButton';
import RMButton from '../../../../components/RMButton';
import { useTheme } from '../../../../ThemeProvider';
import styles from './styles.module.scss';
import { uniqueId } from 'lodash';

const modeOptions = [
  { value: 'host', name: 'Host' },
  { value: 'port', name: 'Port' },
];

const authTypeOptions = [
  { value: 'none', name: 'None' },
  { value: 'basic', name: 'Basic' },
  { value: 'bearer', name: 'Bearer' },
];

const hostProtocolOptions = [
  { value: 'https', name: 'HTTPS' },
];

const portProtocolOptions = [
  { value: 'http', name: 'HTTP' },
  { value: 'tcp', name: 'TCP' },
];

const COMMON_SETUPS_TOOLTIP = [
  'Common setups:',
  '',
  'MinIO object store (port routes)',
  '  s3.gw.example.com:9000 → backend :9000  (port / HTTP)',
  '  console.gw.example.com:9001 → backend :9001  (port / HTTP)',
  '',
  'Database TCP proxy (port route)',
  '  db.gw.example.com:3307 → backend :3306  (port / TCP)',
  '',
  'HTTP API (host route)',
  '  api.example.com → backend :8080  (host / HTTPS)',
  '',
  'You can mix host and port routes in the same app.',
  'Host routes: hostname must be unique on the shared gateway.',
  'Port routes: listener endpoint (cname:sourcePort) must be',
  '  unique across all apps on the shared gateway.',
  '  The gateway resolves the listener CNAME to a bind address',
  '  — wildcard binds are not allowed.',
].join('\n');

function effectiveMode(row) {
  if (row.mode) return row.mode;
  return row.protocol === 'tcp' ? 'port' : 'host';
}

function defaultProtocolForMode(mode) {
  return mode === 'port' ? 'tcp' : 'https';
}

function newRoutePreview(p) {
  const backendPort = p.mode === 'host' ? (p.port || '?') : (p.destPort || '?');
  if (p.mode === 'host') {
    return `${p.protocol || 'https'}://${p.cname || '(public hostname)'}  →  backend :${backendPort}`;
  }
  const listener = p.cname
    ? `${p.cname}:${p.sourcePort || '?'}`
    : `(listener cname):${p.sourcePort || '?'}`;
  return `${listener}  →  backend :${backendPort}`;
}

function isHTTPMonitorCapable(route) {
  const mode = effectiveMode(route);
  const proto = route.protocol || (mode === 'host' ? 'https' : 'tcp');
  return (mode === 'host' && proto === 'https') || (mode === 'port' && proto === 'http');
}

function secretVariableOptions(variables) {
  return (variables || [])
    .filter(v => v.type === 'secret')
    .map(v => ({ value: v.name, name: v.name }));
}

function hasMonitorConfig(route) {
  const m = route.monitor;
  if (!m) return false;
  return !!(m.path || m.authType || m.authUser || m.authSecretVar || m.expectStatus);
}

function normalizeNewRouteMonitorDraft(draft) {
  const { monitorPath, monitorAuthType, monitorAuthUser, monitorAuthSecretVar, monitorExpectStatus, ...routeFields } = draft;
  delete routeFields.id;
  const mode = routeFields.mode || 'host';
  const protocol = routeFields.protocol || (mode === 'host' ? 'https' : 'tcp');
  const httpCapable = (mode === 'host' && protocol === 'https') || (mode === 'port' && protocol === 'http');
  if (!httpCapable) return routeFields;
  const hasMonitor = monitorPath || (monitorAuthType && monitorAuthType !== 'none') || monitorAuthUser || monitorAuthSecretVar || monitorExpectStatus;
  if (!hasMonitor) return routeFields;
  return {
    ...routeFields,
    monitor: {
      path: monitorPath || '',
      authType: monitorAuthType !== 'none' ? (monitorAuthType || '') : '',
      authUser: monitorAuthUser || '',
      authSecretVar: monitorAuthSecretVar || '',
      expectStatus: monitorExpectStatus || '',
    },
  };
}

function routeToEditDraft(route) {
  const mode = effectiveMode(route);
  return {
    mode,
    cname: route.cname || '',
    port: mode === 'host' ? (route.destPort || route.port || '') : '',
    sourcePort: mode === 'port' ? (route.sourcePort || '') : '',
    destPort: mode === 'port' ? (route.destPort || route.port || '') : '',
    protocol: route.protocol || defaultProtocolForMode(mode),
    monitorPath: route.monitor?.path || '',
    monitorAuthType: route.monitor?.authType || 'none',
    monitorAuthUser: route.monitor?.authUser || '',
    monitorAuthSecretVar: route.monitor?.authSecretVar || '',
    monitorExpectStatus: route.monitor?.expectStatus || '',
  };
}

async function resolveRequest(request) {
  if (!request) return request;
  if (typeof request.unwrap === 'function') {
    return request.unwrap();
  }
  return request;
}

function routeKey(route) {
  const mode = effectiveMode(route);
  const cname = route.cname?.toLowerCase() || '';
  const proto = route.protocol || '';
  if (mode === 'port') return `port:${cname}:${route.sourcePort || ''}:${route.destPort || route.port || ''}:${proto}`;
  return `host:${cname}:${route.destPort || route.port || ''}:${proto}`;
}

function matchRouteStatus(route, routeStatuses) {
  if (!routeStatuses?.length) return null;
  const key = routeKey(route);
  for (const rs of routeStatuses) {
    if (routeKey(rs) === key) return rs;
  }
  return null;
}

function modeMeta(route) {
  const isHost = effectiveMode(route) === 'host';
  return {
    isHost,
    label: isHost ? 'Host route' : 'Port route',
    badgeScheme: isHost ? 'purple' : 'teal',
  };
}

function protocolColorScheme(protocol) {
  if (protocol === 'https') return 'green';
  if (protocol === 'http') return 'orange';
  return 'gray';
}

function routeEntryLabel(route) {
  const mode = effectiveMode(route);
  if (mode === 'port') {
    const cname = route.cname || 'listener';
    return `${cname}:${route.sourcePort || '—'}`;
  }
  return route.cname || '—';
}

function routeEntryHint(route) {
  return effectiveMode(route) === 'host'
    ? 'Public hostname on the shared gateway'
    : 'Listener CNAME and source port on the shared gateway';
}

function routeBackendLabel(route) {
  const backendPort = route.destPort || route.port;
  return backendPort ? `:${backendPort}` : '—';
}

function monitorCredentialSummary(monitor) {
  return {
    user: monitor?.authUser ? 'Username configured' : 'No username',
    secret: monitor?.authSecretVar ? 'Secret configured' : 'No secret variable',
  };
}

function actionButtonStyle(kind, theme) {
  const common = {
    minWidth: '34px',
    width: '34px',
    height: '34px',
    padding: '4px',
    borderRadius: '8px',
    borderWidth: '1px',
    borderStyle: 'solid',
    boxShadow: 'none',
  };

  if (theme !== 'dark') return undefined;

  if (kind === 'edit') {
    return {
      ...common,
      backgroundColor: 'rgba(44, 82, 130, 0.16)',
      borderColor: 'rgba(66, 153, 225, 0.45)',
    };
  }

  if (kind === 'delete') {
    return {
      ...common,
      backgroundColor: 'rgba(229, 62, 62, 0.14)',
      borderColor: 'rgba(252, 129, 129, 0.45)',
    };
  }

  return {
    ...common,
    backgroundColor: 'rgba(255, 255, 255, 0.05)',
    borderColor: 'rgba(219, 232, 246, 0.22)',
  };
}

function MetricCard({ label, value, accent }) {
  return (
    <Box
      borderWidth="1px"
      borderColor="var(--quaternary-color)"
      borderRadius="lg"
      px={3}
      py={2}
      bg="var(--secondary-gray-color)"
    >
      <HStack justify="space-between" align="baseline" spacing={3}>
        <Text fontSize="xs" textTransform="uppercase" letterSpacing="widest" color="var(--darkgray-color)">{label}</Text>
        <Text fontSize="lg" fontWeight="semibold" color={accent || 'var(--text-color)'}>
          {value}
        </Text>
      </HStack>
    </Box>
  );
}

MetricCard.propTypes = {
  label: PropTypes.string.isRequired,
  value: PropTypes.oneOfType([PropTypes.string, PropTypes.number]).isRequired,
  accent: PropTypes.string,
}

function StatusBadge({ status, variant = 'subtle' }) {
  if (!status) return <Badge colorScheme="gray" variant={variant} fontSize="2xs" px={1}>—</Badge>;
  if (status === 'AppRunning') return <Badge colorScheme="green" variant={variant} fontSize="2xs" px={1}>Running</Badge>;
  if (status === 'AppWarning') return <Badge colorScheme="yellow" variant={variant} fontSize="2xs" px={1}>Warning</Badge>;
  return <Badge colorScheme="red" variant={variant} fontSize="2xs" px={1}>Failed</Badge>;
}

StatusBadge.propTypes = {
  status: PropTypes.string,
  variant: PropTypes.string,
}

function MonitoringSummary({ route }) {
  const m = route.monitor;
  if (!isHTTPMonitorCapable(route) || !m) return <Text fontSize="xs" color="gray.400">—</Text>;
  const parts = [];
  if (m.path) parts.push(m.path);
  if (m.expectStatus) parts.push(m.expectStatus);
  if (m.authType && m.authType !== 'none') parts.push(m.authType.charAt(0).toUpperCase() + m.authType.slice(1));
  if (!parts.length) return <Text fontSize="xs" color="gray.400">—</Text>;
  return <Text fontSize="xs" color="gray.600" whiteSpace="nowrap">{parts.join(' · ')}</Text>;
}

MonitoringSummary.propTypes = {
  route: PropTypes.shape({
    mode: PropTypes.string,
    protocol: PropTypes.string,
    monitor: PropTypes.shape({
      path: PropTypes.string,
      authType: PropTypes.string,
      authUser: PropTypes.string,
      authSecretVar: PropTypes.string,
      expectStatus: PropTypes.string,
    }),
  }).isRequired,
}

const Routes = React.memo(function Routes({
  gateway = "",
  rows = [],
  variables = [],
  routeStatus = [],
  fieldName = 'routes',
  onRowArrayChange,
  onRowDropIndex,
  onSaveAdd,
  onPauseAutoReload = () => { },
  onResumeAutoReload = () => { },
}) {

  const [formData, setFormData] = useState([]);
  const [expandedMonitorKeys, setExpandedMonitorKeys] = useState(new Set());
  const [editingIndex, setEditingIndex] = useState(null);
  const [editDraft, setEditDraft] = useState(null);
  const { theme } = useTheme();
  const secretVarOpts = secretVariableOptions(variables);
  const panelBg = 'var(--secondary-gray-color)';
  const subtleBg = 'var(--tertiary-color)';
  const borderColor = 'var(--quaternary-color)';
  const mutedColor = 'var(--darkgray-color)';
  const headingColor = 'var(--text-color)';
  const insetBg = 'var(--body-bg-color)';
  const accentColor = 'var(--secondary-color)';
  const badgeVariant = theme === 'dark' ? 'solid' : 'subtle';
  const actionVariant = theme === 'dark' ? 'ghost' : undefined;
  const neutralActionColorScheme = theme === 'dark' ? 'blue' : undefined;
  const neutralActionIconFill = undefined;
  const expandActionColorScheme = theme === 'dark' ? 'whiteAlpha' : undefined;

  const routeMetrics = useMemo(() => {
    const host = rows.filter((route) => effectiveMode(route) === 'host').length;
    const port = rows.length - host;
    const monitored = rows.filter((route) => hasMonitorConfig(route)).length;
    return { total: rows.length, host, port, monitored };
  }, [rows]);

  const toggleMonitorRow = (key) => {
    setExpandedMonitorKeys(prev => {
      const next = new Set(prev);
      if (next.has(key)) next.delete(key); else next.add(key);
      return next;
    });
  };

  const handleArrayChange = (index, key, value) => {
    setFormData(prevState => prevState.map((item, i) => {
      if (i !== index) return item;
      const updated = { ...item, [key]: value };
      if (key === 'mode') {
        updated.protocol = defaultProtocolForMode(value);
        if (value === 'host') {
          updated.port = item.destPort || item.port || '';
          updated.destPort = '';
          updated.sourcePort = '';
        } else {
          updated.destPort = item.port || item.destPort || '';
          updated.port = '';
        }
      }
      return updated;
    }));
  };

  const handleEditDraftChange = (key, value) => {
    setEditDraft((prev) => {
      if (!prev) return prev;
      const updated = { ...prev, [key]: value };
      if (key === 'mode') {
        updated.protocol = defaultProtocolForMode(value);
        if (value === 'host') {
          updated.port = prev.destPort || prev.port || '';
          updated.destPort = '';
          updated.sourcePort = '';
        } else {
          updated.destPort = prev.port || prev.destPort || '';
          updated.port = '';
        }
      }
      return updated;
    });
  };

  const handleAddItem = () => {
    setFormData(prevState => [...prevState, {
      id: uniqueId(),
      mode: 'host',
      cname: '',
      port: '',
      sourcePort: '',
      destPort: '',
      protocol: 'https',
      monitorPath: '',
      monitorAuthType: 'none',
      monitorAuthUser: '',
      monitorAuthSecretVar: '',
      monitorExpectStatus: '',
    }]);
    onPauseAutoReload();
  };

  const handleRemoveItem = (index) => {
    setFormData(prevState => {
      const newState = prevState.filter((_, i) => i !== index);
      if (newState.length === 0) onResumeAutoReload();
      return newState;
    });
  };

  const handleStartEdit = (index, route) => {
    setEditingIndex(index);
    setEditDraft(routeToEditDraft(route));
    onPauseAutoReload();
  };

  const handleCancelEdit = () => {
    setEditingIndex(null);
    setEditDraft(null);
    onResumeAutoReload();
  };

  const handleSaveEdit = async () => {
    if (editingIndex == null || !editDraft) return;

    const normalized = normalizeNewRouteMonitorDraft(editDraft);
    const requests = [];

    requests.push(onRowArrayChange(fieldName, editingIndex, 'mode', normalized.mode));
    requests.push(onRowArrayChange(fieldName, editingIndex, 'cname', normalized.cname || ''));
    requests.push(onRowArrayChange(fieldName, editingIndex, 'protocol', normalized.protocol || defaultProtocolForMode(normalized.mode)));

    if (normalized.mode === 'host') {
      requests.push(onRowArrayChange(fieldName, editingIndex, 'port', normalized.port || ''));
    } else {
      requests.push(onRowArrayChange(fieldName, editingIndex, 'sourcePort', normalized.sourcePort || ''));
      requests.push(onRowArrayChange(fieldName, editingIndex, 'destPort', normalized.destPort || normalized.port || ''));
    }

    if (normalized.monitor) {
      requests.push(onRowArrayChange(fieldName, editingIndex, 'monitor.path', normalized.monitor.path || ''));
      requests.push(onRowArrayChange(fieldName, editingIndex, 'monitor.auth-type', normalized.monitor.authType || ''));
      requests.push(onRowArrayChange(fieldName, editingIndex, 'monitor.auth-user', normalized.monitor.authUser || ''));
      requests.push(onRowArrayChange(fieldName, editingIndex, 'monitor.auth-secret-var', normalized.monitor.authSecretVar || ''));
      requests.push(onRowArrayChange(fieldName, editingIndex, 'monitor.expect-status', normalized.monitor.expectStatus || ''));
    } else {
      requests.push(onRowArrayChange(fieldName, editingIndex, 'monitor.clear', 'true'));
    }

    try {
      for (const request of requests) {
        await resolveRequest(request);
      }
      setEditingIndex(null);
      setEditDraft(null);
      onResumeAutoReload();
    } catch (error) {
      // keep the editor open so the user can correct invalid values
    }
  };

  const handleSaveAdd = () => {
    if (formData.length > 0) {
      const normalized = formData.map(normalizeNewRouteMonitorDraft);
      onSaveAdd(fieldName, normalized).then(() => {
        setFormData([]);
        onResumeAutoReload();
      });
    }
  };

  const sanitizePort = (port) => {
    const n = parseInt(port, 10);
    return isNaN(n) || n < 1 ? '' : n > 65535 ? '65535' : `${n}`;
  };

  return (
    <Flex direction="column" className={`${styles.sectionWrapper}`}>
      <VStack spacing={3} align="stretch">
        <Box borderWidth="1px" borderColor={borderColor} borderRadius="xl" bg={panelBg} p={3}>
          <SimpleGrid columns={{ base: 1, lg: 4 }} spacing={3} alignItems="stretch">
            <Box gridColumn={{ base: 'span 1', lg: 'span 2' }}>
              <VStack spacing={2} align="stretch" h="100%" justify="center">
                <HStack spacing={2} align="center" flexWrap="wrap">
                  <Heading as="h3" size="sm">Domain Gateway</Heading>
                  <Badge colorScheme={gateway ? 'green' : 'gray'} variant={badgeVariant} borderRadius="full" px={2}>
                    {gateway ? 'Configured' : 'Missing'}
                  </Badge>
                </HStack>
                <Text fontSize="sm" color={mutedColor}>
                  Every published route below is exposed through this shared gateway entrypoint.
                </Text>
              </VStack>
            </Box>
            <Box
              gridColumn={{ base: 'span 1', lg: 'span 2' }}
              borderWidth="1px"
              borderColor={borderColor}
              borderRadius="lg"
              bg={panelBg}
              borderLeftWidth="4px"
              borderLeftColor={accentColor}
              px={4}
              py={3}
            >
              <HStack justify="space-between" align="center" spacing={3} flexWrap="wrap">
                <Box>
                  <Text fontSize="xs" textTransform="uppercase" letterSpacing="widest" color="var(--darkgray-color)">Gateway domain</Text>
                  <Text fontWeight="bold" fontFamily="mono" fontSize="sm" color={headingColor}>{gateway || 'Not configured'}</Text>
                </Box>
                {!gateway && (
                  <Text fontSize="xs" color={mutedColor}>Set the gateway domain to expose public hostnames.</Text>
                )}
              </HStack>
            </Box>
          </SimpleGrid>
        </Box>
      </VStack>

      <VStack spacing={3} align="stretch">
        <Box borderWidth="1px" borderColor={borderColor} borderRadius="xl" bg={panelBg} p={3}>
          <VStack spacing={3} align="stretch">
            <SimpleGrid columns={{ base: 1, xl: 5 }} spacing={3} alignItems="start">
              <Box gridColumn={{ base: 'span 1', xl: 'span 2' }}>
                <VStack align="stretch" spacing={2}>
                  <HStack spacing={2} align="center" flexWrap="wrap">
                    <Heading as="h3" size="sm">Route mappings</Heading>
                    <Badge colorScheme="blue" variant={badgeVariant} borderRadius="full" px={2}>{routeMetrics.total}</Badge>
                    <Tooltip
                      label={COMMON_SETUPS_TOOLTIP}
                      placement="right"
                      hasArrow
                      bg="gray.800"
                      color="gray.100"
                      fontSize="xs"
                      fontFamily="mono"
                      whiteSpace="pre"
                      px={3}
                      py={2}
                      borderRadius="md"
                      maxW="420px"
                    >
                      <span><HiInformationCircle size={16} style={{ cursor: 'pointer', color: 'gray' }} /></span>
                    </Tooltip>
                  </HStack>
                  <Text fontSize="sm" color={mutedColor}>
                    Publish app endpoints and attach health checks from one compact view.
                  </Text>
                  {formData.length === 0 && (
                    <HStack pt={1}>
                      <RMButton onClick={handleAddItem}>Add Route</RMButton>
                    </HStack>
                  )}
                </VStack>
              </Box>

              <SimpleGrid gridColumn={{ base: 'span 1', xl: 'span 3' }} columns={{ base: 1, sm: 2, lg: 4 }} spacing={3}>
              <MetricCard label="Total routes" value={routeMetrics.total} />
              <MetricCard label="Host routes" value={routeMetrics.host} accent="purple.500" />
              <MetricCard label="Port routes" value={routeMetrics.port} accent="teal.500" />
              <MetricCard label="Health checks" value={routeMetrics.monitored} accent="blue.500" />
            </SimpleGrid>
            </SimpleGrid>

            {rows?.length > 0 ? (
              <Box borderWidth="1px" borderColor={borderColor} borderRadius="xl" overflow="hidden">
                <Box overflowX="auto">
                  <Table size="sm" variant="simple">
                    <Thead bg={subtleBg}>
                      <Tr>
                        <Th px={3} fontSize="xs">Status</Th>
                        <Th px={3} fontSize="xs">Public entrypoint</Th>
                        <Th px={3} fontSize="xs">Backend</Th>
                        <Th px={3} fontSize="xs">Access</Th>
                        <Th px={3} fontSize="xs">Monitoring</Th>
                      </Tr>
                    </Thead>
                    <Tbody>
                      {rows.map((p, index) => {
                        const { label: modeLabel, badgeScheme } = modeMeta(p);
                        const rowKey = `row_${index}`;
                        const monitorCapable = isHTTPMonitorCapable(p);
                        const matched = matchRouteStatus(p, routeStatus);
                        const rKey = routeKey(p);
                        const monitorExpanded = expandedMonitorKeys.has(rKey);
                        const isEditing = editingIndex === index;
                        const credentialSummary = monitorCredentialSummary(p.monitor);
                        const proto = (p.protocol || '').toUpperCase();

                        return (
                          <React.Fragment key={rowKey}>
                            <Tr>
                              <Td px={3} py={2} verticalAlign="top">
                                <VStack align="flex-start" spacing={2}>
                                  <StatusBadge status={matched?.status} variant={badgeVariant} />
                                  <Badge colorScheme={badgeScheme} variant={badgeVariant} fontSize="2xs" px={2} borderRadius="full">
                                    {modeLabel}
                                  </Badge>
                                </VStack>
                              </Td>
                              <Td px={3} py={2} verticalAlign="top">
                                <VStack align="flex-start" spacing={1}>
                                  <Text fontSize="sm" fontWeight="semibold" fontFamily="mono" color={headingColor}>
                                    {routeEntryLabel(p)}
                                  </Text>
                                  <Text fontSize="xs" color={mutedColor}>{routeEntryHint(p)}</Text>
                                </VStack>
                              </Td>
                              <Td px={3} py={2} verticalAlign="top">
                                <VStack align="flex-start" spacing={1}>
                                  <Text fontSize="sm" fontWeight="semibold" fontFamily="mono" color={headingColor}>
                                    {routeBackendLabel(p)}
                                  </Text>
                                  <Text fontSize="xs" color={mutedColor}>Application backend port</Text>
                                </VStack>
                              </Td>
                              <Td px={3} py={2} verticalAlign="top">
                                <VStack align="flex-start" spacing={2}>
                                  <Badge colorScheme={protocolColorScheme(p.protocol)} variant={badgeVariant} fontSize="2xs" px={2} borderRadius="full">
                                    {proto || '—'}
                                  </Badge>
                                  <Text fontSize="xs" color={mutedColor}>
                                    {monitorCapable ? 'Supports HTTP monitoring' : 'Transport-only route'}
                                  </Text>
                                </VStack>
                              </Td>
                              <Td px={3} py={2} verticalAlign="top">
                                <HStack spacing={2} align="flex-start" justify="space-between">
                                  <VStack align="flex-start" spacing={1}>
                                    <MonitoringSummary route={p} />
                                    {!monitorCapable && (
                                      <Text fontSize="xs" color={mutedColor}>Monitoring details are only available for HTTPS or HTTP routes.</Text>
                                    )}
                                  </VStack>
                                  <HStack spacing={1} px={1} py={1} borderRadius="lg">
                                    <RMIconButton
                                      size="sm"
                                      iconFontsize="1rem"
                                      variant={actionVariant}
                                      colorScheme={neutralActionColorScheme}
                                      iconFillColor={neutralActionIconFill}
                                      style={actionButtonStyle('edit', theme)}
                                      icon={TbEdit}
                                      aria-label="Edit route"
                                      tooltip="Edit route"
                                      onClick={() => handleStartEdit(index, p)}
                                    />
                                    <RMIconButton
                                      size="sm"
                                      iconFontsize="1rem"
                                      variant={actionVariant}
                                      colorScheme={theme === 'dark' ? 'red' : undefined}
                                      style={actionButtonStyle('delete', theme)}
                                      icon={HiTrash}
                                      aria-label="Delete route"
                                      tooltip="Delete route"
                                      onClick={() => onRowDropIndex(fieldName, index)}
                                    />
                                    {monitorCapable && (
                                      <RMIconButton
                                        size="sm"
                                        iconFontsize="1rem"
                                        variant={actionVariant}
                                        colorScheme={expandActionColorScheme}
                                        iconFillColor={neutralActionIconFill}
                                        style={actionButtonStyle('expand', theme)}
                                        icon={monitorExpanded ? HiChevronDown : HiChevronRight}
                                        aria-label={monitorExpanded ? 'Collapse monitoring' : 'View monitoring'}
                                        tooltip={monitorExpanded ? 'Hide monitoring details' : 'Show monitoring details'}
                                        onClick={() => toggleMonitorRow(rKey)}
                                      />
                                    )}
                                  </HStack>
                                </HStack>
                              </Td>
                            </Tr>
                            {monitorExpanded && monitorCapable && (
                              <Tr>
                                <Td colSpan={5} px={3} py={2} bg={panelBg}>
                                  <SimpleGrid columns={{ base: 1, md: 2, xl: 4 }} spacing={3}>
                                    <Box bg={insetBg} borderWidth="1px" borderColor={borderColor} borderRadius="lg" px={3} py={2}>
                                      <Text fontSize="xs" color="gray.500" fontWeight="semibold">Monitor path</Text>
                                      <Text fontSize="sm" fontFamily="mono" color={headingColor}>{p.monitor?.path || '/'}</Text>
                                    </Box>
                                    <Box bg={insetBg} borderWidth="1px" borderColor={borderColor} borderRadius="lg" px={3} py={2}>
                                      <Text fontSize="xs" color="gray.500" fontWeight="semibold">Authentication</Text>
                                      {p.monitor?.authType && p.monitor.authType !== 'none' ? (
                                        <Badge colorScheme="blue" variant={badgeVariant} fontSize="2xs" px={2} borderRadius="full" textTransform="capitalize">
                                          {p.monitor.authType}
                                        </Badge>
                                      ) : (
                                        <Text fontSize="sm" color={mutedColor}>None</Text>
                                      )}
                                    </Box>
                                    <Box bg={insetBg} borderWidth="1px" borderColor={borderColor} borderRadius="lg" px={3} py={2}>
                                      <Text fontSize="xs" color="gray.500" fontWeight="semibold">Auth details</Text>
                                      <Text fontSize="sm" color={mutedColor}>
                                        {credentialSummary.user}
                                      </Text>
                                      <Text fontSize="sm" color={mutedColor}>
                                        {credentialSummary.secret}
                                      </Text>
                                    </Box>
                                    <Box bg={insetBg} borderWidth="1px" borderColor={borderColor} borderRadius="lg" px={3} py={2}>
                                      <Text fontSize="xs" color="gray.500" fontWeight="semibold">Expected status</Text>
                                      <Text fontSize="sm" fontFamily="mono" color={headingColor}>{p.monitor?.expectStatus || 'Default'}</Text>
                                    </Box>
                                  </SimpleGrid>
                                </Td>
                              </Tr>
                            )}
                            {isEditing && editDraft && (
                              <Tr>
                                <Td colSpan={5} px={4} py={4} bg={panelBg} borderTopWidth="1px" borderTopColor={borderColor}>
                                  <Box borderWidth="1px" borderColor={borderColor} borderRadius="xl" bg={insetBg} px={3} py={3}>
                                    <VStack align="stretch" spacing={4}>
                                      <HStack justify="space-between" align="center" flexWrap="wrap">
                                        <Box>
                                          <Text fontSize="sm" fontWeight="semibold">Edit route</Text>
                                          <Text fontSize="xs" color={mutedColor}>Update the published entrypoint and monitoring settings.</Text>
                                        </Box>
                                        <Badge colorScheme="blue" variant={badgeVariant} borderRadius="full" px={2}>Editing saved route</Badge>
                                      </HStack>

                                      <SimpleGrid columns={{ base: 1, md: editDraft.mode === 'host' ? 4 : 5 }} spacing={3}>
                                        <Box>
                                          <Text fontSize="xs" color="gray.500" fontWeight="semibold" mb={1}>Mode</Text>
                                          <Select
                                            value={editDraft.mode}
                                          onChange={(e) => handleEditDraftChange('mode', e.target.value)}
                                        >
                                          {modeOptions.map(opt => (
                                            <option key={opt.value} value={opt.value}>{opt.name}</option>
                                          ))}
                                        </Select>
                                      </Box>
                                      <Box>
                                        <Text fontSize="xs" color="gray.500" fontWeight="semibold" mb={1}>
                                          {editDraft.mode === 'host' ? 'Public hostname' : 'Listener CNAME'}
                                        </Text>
                                        <Input
                                          value={editDraft.cname}
                                          onChange={(e) => handleEditDraftChange('cname', e.target.value)}
                                        />
                                      </Box>
                                      {editDraft.mode === 'port' && (
                                        <Box>
                                          <Text fontSize="xs" color="gray.500" fontWeight="semibold" mb={1}>Source port</Text>
                                          <Input
                                            pattern='^[0-9]{1,5}$'
                                            value={editDraft.sourcePort}
                                            onChange={(e) => handleEditDraftChange('sourcePort', sanitizePort(e.target.value))}
                                          />
                                        </Box>
                                      )}
                                      <Box>
                                        <Text fontSize="xs" color="gray.500" fontWeight="semibold" mb={1}>
                                          {editDraft.mode === 'host' ? 'Backend port' : 'Destination port'}
                                        </Text>
                                        <Input
                                          pattern='^[0-9]{1,5}$'
                                          value={editDraft.mode === 'host' ? editDraft.port : editDraft.destPort}
                                          onChange={(e) => handleEditDraftChange(editDraft.mode === 'host' ? 'port' : 'destPort', sanitizePort(e.target.value))}
                                        />
                                      </Box>
                                      <Box>
                                        <Text fontSize="xs" color="gray.500" fontWeight="semibold" mb={1}>Protocol</Text>
                                        <Select
                                          value={editDraft.protocol}
                                          onChange={(e) => handleEditDraftChange('protocol', e.target.value)}
                                        >
                                          {(editDraft.mode === 'host' ? hostProtocolOptions : portProtocolOptions).map(opt => (
                                            <option key={opt.value} value={opt.value}>{opt.name}</option>
                                          ))}
                                        </Select>
                                        </Box>
                                      </SimpleGrid>

                                      <Box borderWidth="1px" borderColor={borderColor} borderRadius="lg" bg={panelBg} px={4} py={3}>
                                        <Text fontSize="xs" textTransform="uppercase" letterSpacing="widest" color="gray.500" mb={1}>Preview</Text>
                                        <Text fontSize="sm" fontFamily="mono" color={headingColor}>{newRoutePreview(editDraft)}</Text>
                                      </Box>

                                      {(editDraft.mode === 'host' ? editDraft.protocol === 'https' : editDraft.protocol === 'http') ? (
                                        <Box borderWidth="1px" borderColor={borderColor} borderRadius="lg" bg={panelBg} px={3} py={3}>
                                          <Text fontSize="sm" fontWeight="semibold" mb={2}>Monitoring</Text>
                                          <SimpleGrid columns={{ base: 1, md: 2, xl: 4 }} spacing={3}>
                                            <Box>
                                            <Text fontSize="xs" color="gray.500" fontWeight="semibold" mb={1}>Path</Text>
                                            <Input
                                              value={editDraft.monitorPath}
                                              size="sm"
                                              placeholder="/ (default)"
                                              onChange={(e) => handleEditDraftChange('monitorPath', e.target.value)}
                                            />
                                          </Box>
                                            <Box>
                                            <Text fontSize="xs" color="gray.500" fontWeight="semibold" mb={1}>Auth type</Text>
                                            <Select
                                              value={editDraft.monitorAuthType}
                                              size="sm"
                                              onChange={(e) => handleEditDraftChange('monitorAuthType', e.target.value)}
                                            >
                                              {authTypeOptions.map(opt => (
                                                <option key={opt.value} value={opt.value}>{opt.name}</option>
                                              ))}
                                            </Select>
                                            </Box>
                                            {editDraft.monitorAuthType === 'basic' && (
                                              <Box>
                                              <Text fontSize="xs" color="gray.500" fontWeight="semibold" mb={1}>Username</Text>
                                              <Input
                                                value={editDraft.monitorAuthUser}
                                                size="sm"
                                                placeholder="Username"
                                                onChange={(e) => handleEditDraftChange('monitorAuthUser', e.target.value)}
                                              />
                                              </Box>
                                            )}
                                            {(editDraft.monitorAuthType === 'basic' || editDraft.monitorAuthType === 'bearer') && (
                                              <Box>
                                              <Text fontSize="xs" color="gray.500" fontWeight="semibold" mb={1}>Secret variable</Text>
                                              {secretVarOpts.length > 0 ? (
                                                <Select
                                                  value={editDraft.monitorAuthSecretVar}
                                                  size="sm"
                                                  onChange={(e) => handleEditDraftChange('monitorAuthSecretVar', e.target.value)}
                                                >
                                                  <option value="">— secret var —</option>
                                                  {secretVarOpts.map(opt => (
                                                    <option key={opt.value} value={opt.value}>{opt.name}</option>
                                                  ))}
                                                </Select>
                                              ) : (
                                                <Input
                                                  value={editDraft.monitorAuthSecretVar}
                                                  size="sm"
                                                  placeholder="Secret variable name"
                                                  onChange={(e) => handleEditDraftChange('monitorAuthSecretVar', e.target.value)}
                                                />
                                              )}
                                              </Box>
                                            )}
                                            <Box>
                                            <Text fontSize="xs" color="gray.500" fontWeight="semibold" mb={1}>Expected status</Text>
                                            <Input
                                              value={editDraft.monitorExpectStatus}
                                              size="sm"
                                              placeholder="200"
                                              onChange={(e) => handleEditDraftChange('monitorExpectStatus', e.target.value)}
                                            />
                                            </Box>
                                          </SimpleGrid>
                                        </Box>
                                      ) : (
                                        <Text fontSize="sm" color={mutedColor}>
                                          Monitoring can only be configured for HTTPS host routes or HTTP port routes.
                                        </Text>
                                      )}

                                      <HStack flexWrap="wrap">
                                        <RMButton onClick={handleSaveEdit}>Save Changes</RMButton>
                                        <RMButton onClick={handleCancelEdit}>Cancel</RMButton>
                                      </HStack>
                                    </VStack>
                                  </Box>
                                </Td>
                              </Tr>
                            )}
                          </React.Fragment>
                        );
                      })}
                    </Tbody>
                  </Table>
                </Box>
              </Box>
            ) : (
              <Box borderWidth="1px" borderStyle="dashed" borderColor={borderColor} borderRadius="xl" p={6} bg={subtleBg}>
                <VStack spacing={2} align="flex-start">
                  <Heading as="h4" size="xs">No route mappings yet</Heading>
                  <Text fontSize="sm" color={mutedColor}>
                    Add a host or port route to publish this app through the shared gateway.
                  </Text>
                </VStack>
              </Box>
            )}
          </VStack>
        </Box>
      </VStack>

      {formData.length > 0 && (
        <VStack spacing={3} align="stretch">
          <Box borderWidth="1px" borderColor={borderColor} borderRadius="xl" bg={panelBg} p={3}>
            <VStack spacing={3} align="stretch">
              <HStack justify="space-between" align="center" flexWrap="wrap">
                <Box>
                  <Heading as="h3" size="sm">New Route</Heading>
                  <Text fontSize="sm" color={mutedColor}>Draft one or more routes before saving them to the deployment.</Text>
                </Box>
                <Badge colorScheme="blue" variant={badgeVariant} borderRadius="full" px={2}>{formData.length} draft{formData.length > 1 ? 's' : ''}</Badge>
              </HStack>

          {formData.map((p, index) => {
            const isHost = p.mode === 'host';
            const protocolOpts = isHost ? hostProtocolOptions : portProtocolOptions;
            const modeHelp = isHost
              ? 'Clients connect using this hostname over HTTPS via the shared gateway frontend. Traffic is forwarded to the backend port.'
              : 'Clients connect to the listener endpoint (cname:sourcePort). The gateway resolves the listener CNAME to a local bind address — wildcard binds are not allowed.';
            const newMonitorCapable = isHost ? p.protocol === 'https' : p.protocol === 'http';
            const newMonitorAuthType = p.monitorAuthType || 'none';
            return (
              <Box key={`new_${p.id}`} borderWidth="1px" borderColor={borderColor} borderRadius="xl" bg={subtleBg} p={3}>
                <VStack align="stretch" spacing={4}>
                  <HStack justify="space-between" align="flex-start" flexWrap="wrap">
                    <Box>
                      <HStack spacing={2} mb={1}>
                        <Badge colorScheme={isHost ? 'purple' : 'teal'} variant={badgeVariant} borderRadius="full" px={2}>
                          {isHost ? 'Host route' : 'Port route'}
                        </Badge>
                        <Badge colorScheme={protocolColorScheme(p.protocol)} variant={badgeVariant} borderRadius="full" px={2}>
                          {(p.protocol || '').toUpperCase() || '—'}
                        </Badge>
                      </HStack>
                      <Text fontSize="sm" color={mutedColor}>{modeHelp}</Text>
                    </Box>
                    <RMIconButton
                      size="sm"
                      iconFontsize="1rem"
                      icon={HiTrash}
                      variant={actionVariant}
                      colorScheme={theme === 'dark' ? 'red' : undefined}
                      style={actionButtonStyle('delete', theme)}
                      aria-label="Delete Route"
                      tooltip="Remove draft route"
                      onClick={() => handleRemoveItem(index)}
                    />
                  </HStack>

                  <SimpleGrid columns={{ base: 1, md: isHost ? 3 : 4 }} spacing={3}>
                    <Box>
                      <Text fontSize="xs" color="gray.500" fontWeight="semibold" mb={1}>Mode</Text>
                      <Tooltip label={modeHelp} placement="top" hasArrow>
                        <Select
                          value={p.mode}
                          onChange={(e) => handleArrayChange(index, 'mode', e.target.value)}
                        >
                          {modeOptions.map(opt => (
                            <option key={opt.value} value={opt.value}>{opt.name}</option>
                          ))}
                        </Select>
                      </Tooltip>
                    </Box>

                    <Box>
                      <Text fontSize="xs" color="gray.500" fontWeight="semibold" mb={1}>
                        {isHost ? 'Public hostname' : 'Listener CNAME'}
                      </Text>
                      <Input
                        name={`new_${p.id}.cname`}
                        placeholder={isHost ? 'api.example.com' : 'db.gw.example.com'}
                        value={p.cname}
                        onChange={(e) => handleArrayChange(index, 'cname', e.target.value)}
                      />
                    </Box>

                    {!isHost && (
                      <Box>
                        <Text fontSize="xs" color="gray.500" fontWeight="semibold" mb={1}>Source port</Text>
                        <Input
                          name={`new_${p.id}.sourcePort`}
                          pattern='^[0-9]{1,5}$'
                          placeholder="9000"
                          value={p.sourcePort}
                          onChange={(e) => handleArrayChange(index, 'sourcePort', sanitizePort(e.target.value))}
                        />
                      </Box>
                    )}

                    <Box>
                      <Text fontSize="xs" color="gray.500" fontWeight="semibold" mb={1}>
                        {isHost ? 'Backend port' : 'Destination port'}
                      </Text>
                      <Input
                        name={isHost ? `new_${p.id}.port` : `new_${p.id}.destPort`}
                        pattern='^[0-9]{1,5}$'
                        placeholder={isHost ? '8080' : '3306'}
                        value={isHost ? p.port : p.destPort}
                        onChange={(e) => handleArrayChange(index, isHost ? 'port' : 'destPort', sanitizePort(e.target.value))}
                      />
                    </Box>

                    <Box>
                      <Text fontSize="xs" color="gray.500" fontWeight="semibold" mb={1}>Protocol</Text>
                      <Select
                        value={p.protocol}
                        onChange={(e) => handleArrayChange(index, 'protocol', e.target.value)}
                      >
                        {protocolOpts.map(opt => (
                          <option key={opt.value} value={opt.value}>{opt.name}</option>
                        ))}
                      </Select>
                    </Box>
                  </SimpleGrid>

                  <Box borderWidth="1px" borderColor={borderColor} borderRadius="lg" bg={panelBg} px={4} py={3}>
                    <Text fontSize="xs" textTransform="uppercase" letterSpacing="widest" color="gray.500" mb={1}>Preview</Text>
                    <Text fontSize="sm" fontFamily="mono" color={headingColor}>{newRoutePreview(p)}</Text>
                  </Box>

                  <Divider />

                  <Box>
                    <HStack justify="space-between" align="center" flexWrap="wrap" mb={3}>
                      <Box>
                        <Text fontSize="sm" fontWeight="semibold">Monitoring</Text>
                        <Text fontSize="xs" color={mutedColor}>Optional health check settings for HTTP-capable routes.</Text>
                      </Box>
                      {!newMonitorCapable && (
                        <Badge colorScheme="gray" variant={badgeVariant} borderRadius="full" px={2}>Unavailable for this protocol</Badge>
                      )}
                    </HStack>

                    {newMonitorCapable ? (
                      <Box borderLeft="2px solid" borderColor={borderColor} pl={3}>
                        <SimpleGrid columns={{ base: 1, md: 2, xl: 4 }} spacing={3}>
                          <Box>
                            <Text fontSize="xs" color="gray.500" fontWeight="semibold" mb={1}>Path</Text>
                            <Input
                              name={`new_${p.id}.monitor.path`}
                              placeholder="/ (default)"
                              value={p.monitorPath}
                              size="sm"
                              onChange={(e) => handleArrayChange(index, 'monitorPath', e.target.value)}
                            />
                          </Box>
                          <Box>
                            <Text fontSize="xs" color="gray.500" fontWeight="semibold" mb={1}>Auth type</Text>
                            <Select
                              value={p.monitorAuthType}
                              size="sm"
                              onChange={(e) => handleArrayChange(index, 'monitorAuthType', e.target.value)}
                            >
                              {authTypeOptions.map(opt => (
                                <option key={opt.value} value={opt.value}>{opt.name}</option>
                              ))}
                            </Select>
                          </Box>
                          {newMonitorAuthType === 'basic' && (
                            <Box>
                              <Text fontSize="xs" color="gray.500" fontWeight="semibold" mb={1}>Username</Text>
                              <Input
                                name={`new_${p.id}.monitor.auth-user`}
                                placeholder="Username"
                                value={p.monitorAuthUser}
                                size="sm"
                                onChange={(e) => handleArrayChange(index, 'monitorAuthUser', e.target.value)}
                              />
                            </Box>
                          )}
                          {(newMonitorAuthType === 'basic' || newMonitorAuthType === 'bearer') && (
                            <Box>
                              <Text fontSize="xs" color="gray.500" fontWeight="semibold" mb={1}>Secret variable</Text>
                              {secretVarOpts.length > 0 ? (
                                <Select
                                  value={p.monitorAuthSecretVar}
                                  size="sm"
                                  onChange={(e) => handleArrayChange(index, 'monitorAuthSecretVar', e.target.value)}
                                >
                                  <option value="">— secret var —</option>
                                  {secretVarOpts.map(opt => (
                                    <option key={opt.value} value={opt.value}>{opt.name}</option>
                                  ))}
                                </Select>
                              ) : (
                                <Input
                                  name={`new_${p.id}.monitor.auth-secret-var`}
                                  placeholder="Secret variable name"
                                  value={p.monitorAuthSecretVar}
                                  size="sm"
                                  onChange={(e) => handleArrayChange(index, 'monitorAuthSecretVar', e.target.value)}
                                />
                              )}
                            </Box>
                          )}
                          <Box>
                            <Text fontSize="xs" color="gray.500" fontWeight="semibold" mb={1}>Expected status</Text>
                            <Input
                              name={`new_${p.id}.monitor.expect-status`}
                              placeholder="200"
                              value={p.monitorExpectStatus}
                              size="sm"
                              onChange={(e) => handleArrayChange(index, 'monitorExpectStatus', e.target.value)}
                            />
                          </Box>
                        </SimpleGrid>
                        {(newMonitorAuthType === 'basic' || newMonitorAuthType === 'bearer') && secretVarOpts.length === 0 && (
                          <Text fontSize="xs" color={mutedColor} mt={2}>
                            Create the secret first in ENV Variables, then reference it here.
                          </Text>
                        )}
                      </Box>
                    ) : (
                      <Text fontSize="sm" color={mutedColor}>
                        Switch the route to HTTPS or HTTP to configure path-based health checks.
                      </Text>
                    )}
                  </Box>
                </VStack>
              </Box>
            );
          })}
            </VStack>
          </Box>
        </VStack>
      )}

      {formData?.length > 0 && (
        <VStack spacing={3} align="stretch">
          <HStack flexWrap="wrap">
            <RMButton onClick={handleSaveAdd}>Save Route{formData.length > 1 ? 's' : ''}</RMButton>
            <RMButton onClick={handleAddItem}>Add Route</RMButton>
          </HStack>
        </VStack>
      )}
    </Flex>
  );
})

Routes.propTypes = {
  gateway: PropTypes.string,
  rows: PropTypes.array,
  variables: PropTypes.array,
  routeStatus: PropTypes.array,
  fieldName: PropTypes.string,
  onRowArrayChange: PropTypes.func,
  onRowDropIndex: PropTypes.func,
  onSaveAdd: PropTypes.func,
  onPauseAutoReload: PropTypes.func,
  onResumeAutoReload: PropTypes.func,
}

export default Routes
