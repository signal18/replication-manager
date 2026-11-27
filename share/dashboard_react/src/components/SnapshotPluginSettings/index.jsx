import React, { useState } from 'react'
import { Badge, Box, HStack, Input, Text, VStack } from '@chakra-ui/react'
import TextForm from '../TextForm'
import RMSwitch from '../RMSwitch'
import RMButton from '../RMButton'
import { clusterService } from '../../services/clusterService'

// The settings of an external snapshot plugin (#2456), as its manifest declares them: a text
// or a switch per setting, a secret as a write-only field (only whether it is set is shown,
// never its value, and it is never put in a confirmation either). Setting them needs the
// global-settings grant: they point a program on the replication-manager host at a storage
// system and hand it a credential.
function SnapshotPluginSettings({ clusterName, plugin, canEdit, onChanged }) {
  const [secret, setSecret] = useState({})
  const [message, setMessage] = useState(null)
  const settings = plugin?.settings || []
  const held = plugin?.entry?.settings || {}
  const secretsSet = plugin?.entry?.secretsSet || []

  const save = (body) =>
    clusterService
      .setVolumeSnapshotPluginSettings(clusterName, plugin.name, body)
      .then(() => {
        setMessage({ ok: true, text: 'Saved' })
        onChanged && onChanged()
      })
      .catch((err) => setMessage({ ok: false, text: String(err?.response?.data || err?.message || 'Refused') }))

  if (!plugin) return null
  return (
    <VStack align='stretch' spacing={2}>
      <HStack wrap='wrap'>
        {plugin.tier === 'enterprise' && <Badge colorScheme='purple'>enterprise</Badge>}
        {!plugin.allowed && <Badge colorScheme='red'>needs an enterprise plan</Badge>}
        {plugin.tested === false && <Badge colorScheme='orange'>untested on a real storage system</Badge>}
      </HStack>
      {settings.length === 0 && <Text fontSize='sm'>This plugin declares no setting.</Text>}
      {settings.map((st) => (
        <HStack key={st.name} align='center' spacing={3}>
          <Text fontSize='sm' minW='160px'>{st.name}</Text>
          {st.type === 'secret' ? (
            <HStack>
              <Badge colorScheme={secretsSet.includes(st.name) ? 'green' : 'gray'}>{secretsSet.includes(st.name) ? 'set' : 'not set'}</Badge>
              <Input size='sm' type='password' autoComplete='new-password' isDisabled={!canEdit} placeholder='new value'
                value={secret[st.name] || ''} onChange={(e) => setSecret({ ...secret, [st.name]: e.target.value })} />
              <RMButton size='sm' isDisabled={!canEdit || !secret[st.name]}
                onClick={() => { save({ settings: { [st.name]: secret[st.name] } }); setSecret({ ...secret, [st.name]: '' }) }}>Save</RMButton>
              {secretsSet.includes(st.name) && (
                <RMButton size='sm' isDisabled={!canEdit} onClick={() => save({ unset: [st.name] })}>Remove</RMButton>
              )}
            </HStack>
          ) : st.type === 'bool' ? (
            <RMSwitch isChecked={held[st.name] === true || held[st.name] === 'true'} isDisabled={!canEdit}
              confirmTitle={`Confirm ${plugin.name} ${st.name}?`}
              onChange={() => save({ settings: { [st.name]: String(!(held[st.name] === true || held[st.name] === 'true')) } })} />
          ) : (
            <Box flex='1'>
              <TextForm value={held[st.name] !== undefined ? String(held[st.name]) : ''} isDisabled={!canEdit} maxLength={1024}
                confirmTitle={`Confirm ${plugin.name} ${st.name} to `}
                onSave={(value) => save(value === '' ? { unset: [st.name] } : { settings: { [st.name]: value } })} />
            </Box>
          )}
        </HStack>
      ))}
      {message && <Text fontSize='sm' color={message.ok ? 'green.400' : 'red.400'}>{message.text}</Text>}
      {!canEdit && <Text fontSize='xs'>Changed by the instance admin (global-settings).</Text>}
    </VStack>
  )
}

export default SnapshotPluginSettings
