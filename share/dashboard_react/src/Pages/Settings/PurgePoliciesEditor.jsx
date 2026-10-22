import { Flex, Input, Stack, Text } from '@chakra-ui/react'
import React, { useEffect, useState } from 'react'
import RMButton from '../../components/RMButton'

// PurgePoliciesEditor edits purge policies (#2091): rules of a tag and its keep values, written
// "trigger:binlogs keep-within=1d; trigger:snapback keep-last=1" as the cluster's
// backup-keep-tag-policies and an archive's purge-policies hold them. Each rule keeps the
// snapshots carrying its tag, counted apart from the others. allowDefault: a "*" rule (an
// archive's own default) is accepted. replication-manager checks the rules on save.

const KEEP_PATTERN = /^keep-(last|hourly|daily|weekly|monthly|yearly|within(-(hourly|daily|weekly|monthly|yearly))?)=\S+$/

export const parsePurgePolicies = (value) =>
  String(value || '')
    .split(';')
    .map((part) => part.trim().split(/\s+/).filter(Boolean))
    .filter((fields) => fields.length > 0)
    .map(([tag, ...keep]) => ({ tag, keep: keep.join(' ') }))

export const formatPurgePolicies = (rows) =>
  rows
    .filter((r) => r.tag.trim() || r.keep.trim())
    .map((r) => `${r.tag.trim()} ${r.keep.trim()}`.trim())
    .join('; ')

function ruleError(r, allowDefault) {
  if (!r.tag.trim()) return 'a tag is needed'
  if (r.tag.includes('=')) return 'the tag comes first'
  if (r.tag.trim() === '*' && !allowDefault) return 'the default policy is the keep values above'
  const keep = r.keep.trim().split(/\s+/).filter(Boolean)
  if (keep.length === 0) return 'give at least one keep value'
  const bad = keep.find((k) => !KEEP_PATTERN.test(k))
  return bad ? `${bad}: not keep-<last|hourly|daily|weekly|monthly|yearly|within...>=<value>` : ''
}

export default function PurgePoliciesEditor({ value, onSave, isDisabled, allowDefault }) {
  const [rows, setRows] = useState(parsePurgePolicies(value))
  useEffect(() => { setRows(parsePurgePolicies(value)) }, [value])
  const changed = formatPurgePolicies(rows) !== formatPurgePolicies(parsePurgePolicies(value))
  const errors = rows.map((r) => ruleError(r, allowDefault))
  const set = (i, k, v) => setRows((prev) => prev.map((r, j) => (j === i ? { ...r, [k]: v } : r)))

  return (
    <Stack spacing='1' width='100%'>
      {rows.length === 0 && <Text fontSize='sm' opacity='0.7'>None</Text>}
      {rows.map((r, i) => (
        <Stack key={i} spacing='0'>
          <Flex gap='2' align='center'>
            <Input size='sm' maxW='200px' value={r.tag} placeholder={allowDefault ? 'trigger:binlogs or *' : 'trigger:binlogs'}
              isDisabled={isDisabled} onChange={(e) => set(i, 'tag', e.target.value)} />
            <Input size='sm' value={r.keep} placeholder='keep-within=1d keep-last=2' isDisabled={isDisabled}
              onChange={(e) => set(i, 'keep', e.target.value)} />
            <RMButton size='xs' variant='outline' isDisabled={isDisabled} onClick={() => setRows((prev) => prev.filter((_, j) => j !== i))}>Remove</RMButton>
          </Flex>
          {errors[i] && <Text fontSize='xs' color='red.500'>{errors[i]}</Text>}
        </Stack>
      ))}
      <Flex gap='2'>
        <RMButton size='xs' variant='outline' isDisabled={isDisabled} onClick={() => setRows((prev) => [...prev, { tag: '', keep: '' }])}>Add a policy</RMButton>
        {changed && (
          <>
            <RMButton size='xs' isDisabled={isDisabled || errors.some(Boolean)} onClick={() => onSave(formatPurgePolicies(rows))}>Save</RMButton>
            <RMButton size='xs' variant='outline' onClick={() => setRows(parsePurgePolicies(value))}>Cancel</RMButton>
          </>
        )}
      </Flex>
    </Stack>
  )
}
