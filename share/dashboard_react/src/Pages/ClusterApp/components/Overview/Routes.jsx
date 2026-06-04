import { VStack, HStack, Text, Heading, Input, Select, Flex, Tooltip, Code, Box } from '@chakra-ui/react'
import React, { useState } from 'react'
import { HiTrash, HiInformationCircle } from 'react-icons/hi'
import PropTypes from 'prop-types';
import TextForm from '../../../../components/TextForm';
import RMIconButton from '../../../../components/RMIconButton';
import RMButton from '../../../../components/RMButton';
import styles from './styles.module.scss';
import { uniqueId } from 'lodash';
import Dropdown from '../../../../components/Dropdown';

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

function routePreview(p) {
  const mode = effectiveMode(p);
  const backendPort = p.destPort || p.port;
  if (mode === 'host') {
    const proto = p.protocol || 'https';
    const host = p.cname || '(public hostname)';
    return `${proto}://${host}  →  backend :${backendPort || '?'}`;
  }
  const listener = p.cname
    ? `${p.cname}:${p.sourcePort || '?'}`
    : `(listener cname):${p.sourcePort || '?'}`;
  return `${listener}  →  backend :${backendPort || '?'}`;
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
  const { id, monitorPath, monitorAuthType, monitorAuthUser, monitorAuthSecretVar, monitorExpectStatus, ...routeFields } = draft;
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

const Routes = React.memo(function Routes({
  gateway = "",
  rows = [],
  variables = [],
  fieldName = 'routes',
  onRowArrayChange,
  onRowDropIndex,
  onSaveAdd,
  onPauseAutoReload = () => { },
  onResumeAutoReload = () => { },
}) {

  const [formData, setFormData] = useState([]);
  const secretVarOpts = secretVariableOptions(variables);

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
          // cname is kept — host routes use it as the public hostname
        } else {
          updated.destPort = item.port || item.destPort || '';
          updated.port = '';
          // cname is kept — port routes require it as the listener CNAME
        }
      }
      return updated;
    }));
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
        <Heading as="h3" size="md">Domain Gateway</Heading>
        <Text fontWeight={"bold"}>{gateway}</Text>
      </VStack>

      <VStack spacing={3} align="stretch">
        <HStack spacing={2} align="center">
          <Heading as="h3" size="md">Route mappings</Heading>
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
        <Text fontSize="sm" color="gray.600">
          Each row exposes one public entrypoint.{' '}
          <strong>Host</strong> routes by hostname (HTTPS) via the shared gateway frontend.{' '}
          <strong>Port</strong> routes by listener hostname + port (TCP or HTTP) — the gateway binds the
          resolved listener address; wildcard binds are not allowed.{' '}
          You can mix both in the same app.
        </Text>

        {rows?.length > 0
          ? rows.map((p, index) => {
            const mode = effectiveMode(p);
            const isHost = mode === 'host';
            const protocolOpts = isHost ? hostProtocolOptions : portProtocolOptions;
            const rowKey = `row_${index}`;
            const availableModeOptions = !isHost && !p.cname
              ? modeOptions.filter(o => o.value !== 'host')
              : modeOptions;
            const modeHelp = isHost
              ? 'Clients connect using this hostname over HTTPS via the shared gateway frontend. Traffic is forwarded to the backend port.'
              : 'Clients connect to the listener endpoint (cname:sourcePort). The gateway resolves the listener CNAME to a local bind address — wildcard binds are not allowed.';
            const monitorCapable = isHTTPMonitorCapable(p);
            const authType = p.monitor?.authType || 'none';
            const savedSecretVarOpts = [{ value: '__none__', name: '— none —' }, ...secretVarOpts];
            return (
              <VStack key={rowKey} align="stretch" spacing={1}>
                <HStack align="center">
                  <Tooltip label={modeHelp} placement="top" hasArrow>
                    <span>
                      <Dropdown
                        confirmTitle={"Route mode changed — protocol will be reset to the new mode's default"}
                        name={`${rowKey}.mode`}
                        selectedValue={mode}
                        onChange={(value) => onRowArrayChange(fieldName, index, "mode", value)}
                        options={availableModeOptions}
                      />
                    </span>
                  </Tooltip>
                  {isHost ? (
                    <>
                      <TextForm
                        confirmTitle={"Public hostname changed"}
                        name={`${rowKey}.cname`}
                        placeholder="Public hostname"
                        value={p.cname}
                        onSave={(value) => onRowArrayChange(fieldName, index, "cname", value)}
                      />
                      <TextForm
                        confirmTitle={"Backend port changed"}
                        pattern='^[0-9]{1,5}$'
                        name={`${rowKey}.port`}
                        placeholder="Backend port"
                        value={p.destPort || p.port}
                        onSave={(value) => onRowArrayChange(fieldName, index, "port", sanitizePort(value))}
                      />
                    </>
                  ) : (
                    <>
                      <TextForm
                        confirmTitle={"Listener CNAME changed — gateway will re-resolve the bind address on next reconcile"}
                        name={`${rowKey}.cname`}
                        placeholder="Listener CNAME"
                        value={p.cname}
                        onSave={(value) => onRowArrayChange(fieldName, index, "cname", value)}
                      />
                      <TextForm
                        confirmTitle={"Source port changed"}
                        pattern='^[0-9]{1,5}$'
                        name={`${rowKey}.sourcePort`}
                        placeholder="Source port"
                        value={p.sourcePort}
                        onSave={(value) => onRowArrayChange(fieldName, index, "sourceport", sanitizePort(value))}
                      />
                      <TextForm
                        confirmTitle={"Destination port changed"}
                        pattern='^[0-9]{1,5}$'
                        name={`${rowKey}.destPort`}
                        placeholder="Dest port"
                        value={p.destPort || p.port}
                        onSave={(value) => onRowArrayChange(fieldName, index, "destport", sanitizePort(value))}
                      />
                    </>
                  )}
                  <Dropdown
                    confirmTitle={"Protocol changed"}
                    name={`${rowKey}.protocol`}
                    selectedValue={p.protocol}
                    onChange={(value) => onRowArrayChange(fieldName, index, "protocol", value)}
                    options={protocolOpts}
                  />
                  <RMIconButton icon={HiTrash} aria-label="Delete Route" onClick={() => onRowDropIndex(fieldName, index)} />
                </HStack>
                <Code fontSize="xs" color="gray.500" bg="transparent" pl={1}>{routePreview(p)}</Code>
                {monitorCapable && (
                  <Box pl={1} pt={1} borderLeft="2px solid" borderColor="gray.200">
                    <HStack spacing={2} flexWrap="wrap" align="flex-start">
                      <Text fontSize="xs" color="gray.400" fontWeight="semibold" minW="75px" pt={1}>Monitoring</Text>
                      <TextForm
                        confirmTitle="Monitor path changed"
                        name={`${rowKey}.monitor.path`}
                        placeholder="/health"
                        value={p.monitor?.path || ''}
                        onSave={(value) => onRowArrayChange(fieldName, index, 'monitor.path', value)}
                      />
                      <Dropdown
                        confirmTitle="Monitor auth type changed"
                        name={`${rowKey}.monitor.auth-type`}
                        selectedValue={authType}
                        onChange={(value) => onRowArrayChange(fieldName, index, 'monitor.auth-type', value)}
                        options={authTypeOptions}
                      />
                      {authType === 'basic' && (
                        <TextForm
                          confirmTitle="Monitor auth user changed"
                          name={`${rowKey}.monitor.auth-user`}
                          placeholder="Username"
                          value={p.monitor?.authUser || ''}
                          onSave={(value) => onRowArrayChange(fieldName, index, 'monitor.auth-user', value)}
                        />
                      )}
                      {(authType === 'basic' || authType === 'bearer') && (
                        <Dropdown
                          confirmTitle="Monitor secret variable changed"
                          name={`${rowKey}.monitor.auth-secret-var`}
                          selectedValue={p.monitor?.authSecretVar || '__none__'}
                          onChange={(value) => onRowArrayChange(fieldName, index, 'monitor.auth-secret-var', value === '__none__' ? '' : value)}
                          options={savedSecretVarOpts}
                          placeholder="Secret var"
                        />
                      )}
                      <TextForm
                        confirmTitle="Expected status codes changed"
                        name={`${rowKey}.monitor.expect-status`}
                        placeholder="200"
                        value={p.monitor?.expectStatus || ''}
                        onSave={(value) => onRowArrayChange(fieldName, index, 'monitor.expect-status', value)}
                      />
                      {hasMonitorConfig(p) && (
                        <RMButton size="xs" variant="outline" onClick={() => onRowArrayChange(fieldName, index, 'monitor.clear', 'true')}>
                          Clear Monitoring
                        </RMButton>
                      )}
                    </HStack>
                  </Box>
                )}
              </VStack>
            );
          })
          : <Text>No saved route mappings</Text>
        }
      </VStack>

      {formData.length > 0 && (
        <VStack spacing={3} align="stretch">
          <Heading as="h3" size="md">New Route</Heading>
          {formData.map((p, index) => {
            const isHost = p.mode === 'host';
            const protocolOpts = isHost ? hostProtocolOptions : portProtocolOptions;
            const modeHelp = isHost
              ? 'Clients connect using this hostname over HTTPS via the shared gateway frontend. Traffic is forwarded to the backend port.'
              : 'Clients connect to the listener endpoint (cname:sourcePort). The gateway resolves the listener CNAME to a local bind address — wildcard binds are not allowed.';
            const newMonitorCapable = isHost ? p.protocol === 'https' : p.protocol === 'http';
            const newMonitorAuthType = p.monitorAuthType || 'none';
            return (
              <VStack key={`new_${p.id}`} align="stretch" spacing={1}>
                <HStack align="center">
                  <Tooltip label={modeHelp} placement="top" hasArrow>
                    <Select
                      value={p.mode}
                      onChange={(e) => handleArrayChange(index, 'mode', e.target.value)}
                      maxW="110px"
                    >
                      {modeOptions.map(opt => (
                        <option key={opt.value} value={opt.value}>{opt.name}</option>
                      ))}
                    </Select>
                  </Tooltip>
                  {isHost ? (
                    <>
                      <Input
                        name={`new_${p.id}.cname`}
                        placeholder="Public hostname"
                        value={p.cname}
                        onChange={(e) => handleArrayChange(index, 'cname', e.target.value)}
                      />
                      <Input
                        name={`new_${p.id}.port`}
                        pattern='^[0-9]{1,5}$'
                        placeholder="Backend port"
                        value={p.port}
                        onChange={(e) => handleArrayChange(index, 'port', sanitizePort(e.target.value))}
                      />
                    </>
                  ) : (
                    <>
                      <Input
                        name={`new_${p.id}.cname`}
                        placeholder="Listener CNAME"
                        value={p.cname}
                        onChange={(e) => handleArrayChange(index, 'cname', e.target.value)}
                      />
                      <Input
                        name={`new_${p.id}.sourcePort`}
                        pattern='^[0-9]{1,5}$'
                        placeholder="Source port"
                        value={p.sourcePort}
                        onChange={(e) => handleArrayChange(index, 'sourcePort', sanitizePort(e.target.value))}
                      />
                      <Input
                        name={`new_${p.id}.destPort`}
                        pattern='^[0-9]{1,5}$'
                        placeholder="Dest port"
                        value={p.destPort}
                        onChange={(e) => handleArrayChange(index, 'destPort', sanitizePort(e.target.value))}
                      />
                    </>
                  )}
                  <Select
                    value={p.protocol}
                    onChange={(e) => handleArrayChange(index, 'protocol', e.target.value)}
                    maxW="100px"
                  >
                    {protocolOpts.map(opt => (
                      <option key={opt.value} value={opt.value}>{opt.name}</option>
                    ))}
                  </Select>
                  <RMIconButton icon={HiTrash} aria-label="Delete Route" onClick={() => handleRemoveItem(index)} />
                </HStack>
                <Code fontSize="xs" color="gray.500" bg="transparent" pl={1}>{newRoutePreview(p)}</Code>
                {newMonitorCapable && (
                  <Box pl={1} pt={1} borderLeft="2px solid" borderColor="gray.200">
                    <HStack spacing={2} flexWrap="wrap" align="flex-start">
                      <Text fontSize="xs" color="gray.400" fontWeight="semibold" minW="75px" pt={1}>Monitoring</Text>
                      <Input
                        name={`new_${p.id}.monitor.path`}
                        placeholder="/health"
                        value={p.monitorPath}
                        size="sm"
                        maxW="160px"
                        onChange={(e) => handleArrayChange(index, 'monitorPath', e.target.value)}
                      />
                      <Select
                        value={p.monitorAuthType}
                        size="sm"
                        maxW="110px"
                        onChange={(e) => handleArrayChange(index, 'monitorAuthType', e.target.value)}
                      >
                        {authTypeOptions.map(opt => (
                          <option key={opt.value} value={opt.value}>{opt.name}</option>
                        ))}
                      </Select>
                      {newMonitorAuthType === 'basic' && (
                        <Input
                          name={`new_${p.id}.monitor.auth-user`}
                          placeholder="Username"
                          value={p.monitorAuthUser}
                          size="sm"
                          maxW="140px"
                          onChange={(e) => handleArrayChange(index, 'monitorAuthUser', e.target.value)}
                        />
                      )}
                      {(newMonitorAuthType === 'basic' || newMonitorAuthType === 'bearer') && (
                        secretVarOpts.length > 0 ? (
                          <Select
                            value={p.monitorAuthSecretVar}
                            size="sm"
                            maxW="180px"
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
                            maxW="180px"
                            onChange={(e) => handleArrayChange(index, 'monitorAuthSecretVar', e.target.value)}
                          />
                        )
                      )}
                      <Input
                        name={`new_${p.id}.monitor.expect-status`}
                        placeholder="200"
                        value={p.monitorExpectStatus}
                        size="sm"
                        maxW="120px"
                        onChange={(e) => handleArrayChange(index, 'monitorExpectStatus', e.target.value)}
                      />
                    </HStack>
                    {(newMonitorAuthType === 'basic' || newMonitorAuthType === 'bearer') && secretVarOpts.length === 0 && (
                      <Text fontSize="xs" color="gray.400" pl={1} mt={1}>
                        Create the secret first in ENV Variables, then reference it here.
                      </Text>
                    )}
                  </Box>
                )}
              </VStack>
            );
          })}
        </VStack>
      )}

      <VStack spacing={3} align="stretch">
        <HStack>
          {formData?.length > 0 && (
            <RMButton onClick={handleSaveAdd}>Save Route</RMButton>
          )}
          <RMButton onClick={handleAddItem}>Add Route</RMButton>
        </HStack>
      </VStack>
    </Flex>
  );
})

Routes.propTypes = {
  gateway: PropTypes.string,
  rows: PropTypes.array,
  variables: PropTypes.array,
  fieldName: PropTypes.string,
  onRowArrayChange: PropTypes.func,
  onRowDropIndex: PropTypes.func,
  onSaveAdd: PropTypes.func,
  onPauseAutoReload: PropTypes.func,
  onResumeAutoReload: PropTypes.func,
}

export default Routes
