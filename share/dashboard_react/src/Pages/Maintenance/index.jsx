import { createColumnHelper } from '@tanstack/react-table'
import React, { useEffect, useMemo, useState } from 'react'
import { sizeOf, convertObjectToArray, formatBytes, formatDate, getBackupMethod, getBackupStrategy } from '../../utility/common'
import AccordionComponent from '../../components/AccordionComponent'
import { DataTable } from '../../components/DataTable'
import styles from './styles.module.scss'
import { Box, Checkbox, Flex, HStack, Input, Progress, Slider, SliderFilledTrack, SliderThumb, SliderTrack, Tooltip, useDisclosure, VStack } from '@chakra-ui/react'
import TableType3 from '../../components/TableType3'
import { useDispatch, useSelector } from 'react-redux'
import { TaskLogs } from '../Dashboard/components/Logs'
import DatabaseJobs from './DatabaseJobs'
import AttachedArchive from './AttachedArchive'
import { clusterService } from '../../services/clusterService'
import { archiveBackup, deleteBackup, discoverBackups, registerBackup, registerBackupChange, unregisterBackup, reseedFromBackup, verifyBackup, purgeResticSnapshot, resticQueueCancel, resticQueueMove, resticQueuePause, resticQueueResume } from '../../redux/clusterSlice'
import RMIconButton from '../../components/RMIconButton'
import CopyToClipboard from '../../components/CopyToClipboard'
import ConfirmModal from '../../components/Modals/ConfirmModal'
import { HiCog, HiPause, HiPlay, HiTrash, HiRewind, HiQuestionMarkCircle, HiSearch, HiDocumentAdd } from 'react-icons/hi'
import Dropdown from '../../components/Dropdown'
import CommonModal from '../../components/Modals/CommonModal'
import modalStyles from '../../components/Modals/styles.module.scss'
import Markdown from 'react-markdown'
import remarkGfm from 'remark-gfm'
import { showWarningToast } from '../../redux/toastSlice'
import PropTypes from 'prop-types'
import { changePlanUnits } from '../../redux/settingsSlice'
import { Text } from '@chakra-ui/react'
import TextForm from '../../components/TextForm'
import TagPill from '../../components/TagPill'
import MenuOptions from '../../components/MenuOptions'
import { getUnitRatios } from '../../utility/unitRatios'

const QueueMoveForm = React.memo(({ list = [], currentId, onChange = (dir, afterId) => { } }) => {
  const [direction, setDirection] = useState('first');

  const handleDirectionChange = (e) => {
    setDirection(e.target.value);
    if (e.target.value !== 'after') {
      onChange(e.target.value, null);
    }
  };

  const handleAfterChange = (e) => {
    onChange('after', e.target.value);
  };
  return (
    <VStack>
      <HStack>
        <Box>Move {direction}:</Box>
        <Select onChange={handleDirectionChange} value={direction}>
          <option value="first">First</option>
          <option value="before">After</option>
          <option value="last">Last</option>
        </Select>
      </HStack>
      {direction === 'after' && (
        <HStack>
          <Box>Move After:</Box>
          <Select onChange={handleAfterChange}>
            {list.filter(item => item.task_id !== currentId).map(item => (
              <option key={item.task_id} value={item.task_id}>Task ID #{item.task_id}</option>
            ))}
          </Select>
        </HStack>
      )}
    </VStack>
  )
})

const formConfig = {
  queueMove: {
    component: QueueMoveForm,
    getProps: (ctx) => ({
      list: ctx.queueData,
      currentId: ctx.payload.data.taskId,
      onChange: ctx.handleMove
    })
  }
};

const DynamicForm = (ctx) => {
  const { action } = ctx.payload;

  const entry = formConfig[action];
  if (!entry) return null;

  const Component = entry.component;
  const props = entry.getProps(ctx);

  return <Component {...props} />;
};

const resticTaskType = (rtt) => {
  switch (rtt) {
    case 0:
      return "init"
    case 1:
      return "fetch"
    case 2:
      return "backup"
    case 3:
      return "purge"
    case 4:
      return "unlock"
    case 5:
      return "changepass"
    case 6:
      return "restore"
    case 7:
      return "check"
    case 8:
      return "copy"
    default:
      return "Unknown"
  }
}

const resticTaskDetail = (row) => {
  switch (row.task_type) {
    case 2:
      return (<VStack>
        <HStack>
          <Box>Path:</Box>
          <Box>{row.dir_path}</Box>
        </HStack>
        <HStack>
          <Box>Tags:</Box>
          <Box>{row.tags?.join(', ')}</Box>
        </HStack>
      </VStack>)
    case 3:
      return (<VStack>
        <HStack>
          <Box>Options:</Box>
          <Box>{JSON.stringify(row.opt)}</Box>
        </HStack>
      </VStack>)
    default:
      return (<div>-</div>)
  }
}


// BKUSlider is the backup plan bar, the BKU twin of the configurator's DBUSlider: the
// per-cluster reservation prov-db-bku on a linear 1..BKU_MAX scale. What the user must read
// at a glance is the GB LIMIT the plan gives them and how much of it is used: the plan in
// GB and the usage bar in GB against it. No pricing here: the product is not only a cloud
// offer; what a unit costs, when it does, is the marketplace's business (Resource Manager).
const BKU_MAX = 128
const bkuHelp = (unitGB) => `**Backup storage plan (BKU)**

1 BKU = ${unitGB} GB of local backup storage: the replication-manager backups kept on the infrastructure and the extra physical disk replicated for failover applications.

Exceeding the plan is monitored as over-commit and raises the alert WARN0225.`
function BKUSlider({ value, isDisabled, onChange, unitGB, bku }) {
  const [draft, setDraft] = useState(null)
  const [showTooltip, setShowTooltip] = useState(false)
  const [isHelpOpen, setIsHelpOpen] = useState(false)
  const plan = draft !== null ? draft : value
  const planGB = plan * unitGB
  const usedBytes = bku ? (bku.localBytes || 0) + (bku.appDiskBytes || 0) : 0
  const usedGB = usedBytes / (1024 * 1024 * 1024)
  const pct = planGB > 0 ? (usedGB / planGB) * 100 : 0
  const fmt = (n) => `${n} BKU = ${n * unitGB} GB of local backup storage`
  return (
    <Box w='100%'>
      <Flex justify='space-between' mb={1} align='start'>
        <HStack spacing={1}>
          <Text fontSize='sm' fontWeight='bold' color='var(--text-color)'>Backup storage plan (BKU) — per cluster</Text>
          <RMIconButton icon={HiQuestionMarkCircle} onClick={() => setIsHelpOpen(true)} iconFontsize='1rem' variant='ghost' style={{ opacity: 0.5, minWidth: '1.5rem', height: '1.5rem' }} />
        </HStack>
        <Text fontSize='sm' fontWeight='semibold' color='var(--text-color)'>plan {plan} BKU = {planGB} GB</Text>
      </Flex>
      <Slider
        min={1}
        max={BKU_MAX}
        step={1}
        value={plan}
        isDisabled={isDisabled}
        onChange={(v) => setDraft(v)}
        onMouseEnter={() => setShowTooltip(true)}
        onMouseLeave={() => setShowTooltip(false)}
        onChangeEnd={(v) => {
          setDraft(null)
          if (v !== value && onChange) onChange(v)
        }}
      >
        <SliderTrack h='8px' borderRadius='full' bg='gray.200'>
          <SliderFilledTrack bg='blue.400' />
        </SliderTrack>
        <Tooltip label={fmt(plan)} placement='top' isOpen={showTooltip || draft !== null} hasArrow>
          <SliderThumb boxSize={5} bg='blue.500' />
        </Tooltip>
      </Slider>
      <Flex justify='space-between' mt={1}>
        <Text fontSize='9px' color='gray.500'>1 BKU = {unitGB} GB</Text>
        <Text fontSize='9px' color='gray.500'>{BKU_MAX} BKU = {BKU_MAX * unitGB} GB</Text>
      </Flex>
      {bku && (
        <Box mt={2}>
          <Flex justify='space-between' mb={1}>
            <Text fontSize='sm' color='var(--text-color)'>
              Used {usedGB.toFixed(1)} GB of {planGB} GB ({pct.toFixed(0)}%) — backups {formatBytes(bku.localBytes || 0)}{bku.appDiskBytes > 0 ? `, failover application disks ${formatBytes(bku.appDiskBytes)}` : ''}
            </Text>
            <Text fontSize='sm' fontWeight='semibold' color={bku.overPlanUnits > 0 ? 'red.500' : 'var(--text-color)'}>
              {bku.overPlanUnits > 0
                ? `${bku.overPlanUnits * unitGB} GB over the plan (${bku.overPlanUnits} BKU), WARN0225 open`
                : bku.underPlanUnits > 0
                  ? `${bku.underPlanUnits * unitGB} GB left in the plan (${bku.underPlanUnits} BKU)`
                  : 'at the plan'}
            </Text>
          </Flex>
          <Progress value={Math.min(pct, 100)} size='sm' borderRadius='full' colorScheme={pct >= 100 ? 'red' : pct >= 80 ? 'orange' : 'blue'} />
        </Box>
      )}
      <CommonModal isOpen={isHelpOpen} closeModal={() => setIsHelpOpen(false)} title='Backup storage plan (BKU)' body={<Box className={modalStyles.infoTooltip}><Markdown remarkPlugins={[remarkGfm]}>{bkuHelp(unitGB)}</Markdown></Box>} size='xl' />
    </Box>
  )
}
BKUSlider.propTypes = {
  value: PropTypes.number,
  isDisabled: PropTypes.bool,
  onChange: PropTypes.func,
  unitGB: PropTypes.number,
  bku: PropTypes.object,
}

// BackupCellLine shows one labelled value of a backup on a single line, cut at the column
// width: shown is what fits (the value by default), more the number of entries left out,
// the tooltip gives the value whole and the copy button copies it as is.
// the archives holding a backup, by name, with their snapshots (#2091): one detached is
// restored from once attached
function backupArchivesTooltip(row, attached) {
  const refs = row.archiveRefs || {}
  const names = Object.keys(refs).sort()
  if (names.length === 0) return row.resticSnapshotID ? `snapshot ${row.resticSnapshotID}` : ''
  return names.map((n) => `${n}: snapshot ${refs[n]}${attached && n !== attached ? ' (detached: attach it to restore from it)' : ''}`).join('\n')
}

const backupLocationText = {
  local: 'On replication-manager\'s own disk',
  remote: 'In the bucket, read through the backups folder mount',
  archive: 'Only in a snapshot of the attached archive, fetched back before a restore'
}

// backupDuration is how long the backup took, from its start to its end
function backupDuration(row) {
  const start = Date.parse(row.startTime)
  const end = Date.parse(row.endTime)
  if (!start || !end || row.endTime.startsWith('0001') || end < start) return ''
  let secs = Math.round((end - start) / 1000)
  const h = Math.floor(secs / 3600)
  const m = Math.floor((secs % 3600) / 60)
  secs %= 60
  return [h && `${h}h`, (h || m) && `${m}m`, `${secs}s`].filter(Boolean).join(' ')
}

const lower = (s) => (s || '').toLowerCase()

// backupTags are a backup's attributes and states as readable tags (#2091): what it is, what
// it gives a restore, where its file is, and its states; a tooltip only details a tag
function backupTags(row, change, attached) {
  const v = row.verification
  const caps = (row.capabilities || []).filter((c) => c !== 'compress' && c !== 'encrypt')
  const verify = () => {
    if (!v) return null
    const tip = verificationLabel(v) + (verificationBeforeChange(row, change) ? ' · taken before the file changed' : '')
    switch (v.status) {
      case 'running':
        return { text: 'verifying', colorScheme: 'yellow', tooltip: tip }
      case 'skipped':
        return { text: 'verify skipped', colorScheme: 'gray', tooltip: tip }
      case 'passed':
        if (v.warnings?.length) return { text: 'verified with warnings', colorScheme: 'orange', tooltip: tip }
        return { text: v.level === 'restore' ? 'test restored' : 'verified', colorScheme: 'green', tooltip: tip }
      default:
        return { text: 'verify failed', colorScheme: 'red', tooltip: tip }
    }
  }
  return [
    { text: row.backupTool, colorScheme: 'blue' },
    { text: lower(getBackupMethod(row.backupMethod)), colorScheme: 'blue' },
    { text: lower(getBackupStrategy(row.backupStrategy)), colorScheme: 'blue' },
    row.role && { text: row.role, colorScheme: 'purple', tooltip: `The source server was ${row.role} when the backup started` },
    row.compressed && { text: 'compressed', colorScheme: 'gray' },
    row.encrypted && { text: 'encrypted', colorScheme: 'gray', tooltip: row.encryptionAlgo },
    ...caps.map((c) => ({ text: c, colorScheme: 'gray' })),
    row.location && { text: row.location, colorScheme: 'cyan', tooltip: backupLocationText[row.location] },
    (row.archived || Object.keys(row.archiveRefs || {}).length > 0) && {
      text: 'archived',
      colorScheme: 'cyan',
      tooltip: backupArchivesTooltip(row, attached)
    },
    row.completed
      ? { text: 'completed', colorScheme: 'green' }
      : row.discoveryNote
        ? { text: 'not completed', colorScheme: 'red', tooltip: row.discoveryNote }
        : { text: 'pending', colorScheme: 'yellow' },
    verify(),
    row.proven && { text: 'proven', colorScheme: 'green', tooltip: 'A test restore loaded its file, unchanged since' },
    row.restoreCandidate && {
      text: 'picked now',
      colorScheme: 'green',
      tooltip: 'Its server\'s last backup of its kind: a restore or reseed chooses among those'
    },
    row.discovered && {
      text: 'registered',
      colorScheme: 'gray',
      tooltip: `Registered from a backup folder${row.registration?.adopted ? ' (by a previous release)' : ''}: never used by an automatic restore`
    },
    row.notABackup && { text: 'not a backup', colorScheme: 'red', tooltip: `${row.discoveryNote}. Never used: unregister it` },
    change && {
      text: 'changed',
      colorScheme: 'red',
      tooltip: `${change.describe} (WARN0249). Not used until the change is registered${change.registerError ? `. Last register refused: ${change.registerError}` : ''}`
    }
  ].filter((t) => t && t.text)
}

function BackupTags({ tags }) {
  return (
    <Flex wrap='wrap' gap={1}>
      {tags.map((t) =>
        t.tooltip ? (
          <Tooltip key={t.text} label={<Box whiteSpace='pre'>{t.tooltip}</Box>} hasArrow maxW='none'>
            <Box as='span' display='inline-block'>
              <TagPill text={t.text} colorScheme={t.colorScheme} />
            </Box>
          </Tooltip>
        ) : (
          <TagPill key={t.text} text={t.text} colorScheme={t.colorScheme} />
        )
      )}
    </Flex>
  )
}
BackupTags.propTypes = { tags: PropTypes.array }

// the log copies of the backup's server directory (backup-binlogs): number, lowest, highest
function BackupLogsLine({ copies }) {
  if (!copies) return null
  const at = (s) => (s ? new Date(s * 1000).toLocaleString() : '')
  const text = copies.count ? `Logs: ${copies.count} · min ${copies.min} · max ${copies.max}` : 'Logs: none'
  const tip = copies.count
    ? `${copies.count} binary log copies in the server's backup directory, ${formatBytes(copies.totalBytes || 0)}\nlowest ${copies.min}, first event ${at(copies.minStart)}\nhighest ${copies.max}, first event ${at(copies.maxStart)}`
    : 'No binary log copy in the server\'s backup directory (backup-binlogs)'
  return (
    <Tooltip label={<Box whiteSpace='pre'>{tip}</Box>} hasArrow placement='bottom-start' maxW='none'>
      <Box className={styles.cellValue}>{text}</Box>
    </Tooltip>
  )
}
BackupLogsLine.propTypes = { copies: PropTypes.object }

function BackupCellLine({ label, value, shown, more = 0, tooltip, textType }) {
  if (!value) return null
  return (
    <Tooltip label={<Box whiteSpace='pre'>{tooltip || value}</Box>} hasArrow placement='bottom-start' maxW='none'>
      <Box>
        <CopyToClipboard text={value} textType={textType} copyIconPosition='end' className={styles.lineCopy}>
          <HStack spacing={1} className={styles.cellLine}>
            <Box className={styles.cellLabel}>{label}</Box>
            <Box className={styles.cellLineValue}>{shown || value}</Box>
            {more > 0 && <Box className={styles.cellMore}>+{more}</Box>}
          </HStack>
        </CopyToClipboard>
      </Box>
    </Tooltip>
  )
}

BackupCellLine.propTypes = {
  label: PropTypes.string,
  value: PropTypes.string,
  shown: PropTypes.string,
  more: PropTypes.number,
  tooltip: PropTypes.string,
  textType: PropTypes.string
}

// gtidInlineMax: a GTID set up to this length is shown whole (MariaDB, a young MySQL
// cluster); a longer one shows its first entry and the number of the others.
const gtidInlineMax = 48

// BackupGtid shows a backup's GTID set: whole when it is short, else its first entry and a
// +N chip; the tooltip lists every entry.
function BackupGtid({ gtid }) {
  const set = (gtid || '').trim()
  const entries = set.split(',').map((e) => e.trim()).filter(Boolean)
  const whole = set.length <= gtidInlineMax
  return (
    <BackupCellLine
      label='GTID:'
      value={set}
      shown={whole ? set : entries[0]}
      more={whole ? 0 : entries.length - 1}
      tooltip={entries.join(',\n')}
      textType='GTID'
    />
  )
}

BackupGtid.propTypes = {
  gtid: PropTypes.string
}

// backupDestName is the last part of a backup's destination (its file or directory): the
// directories before it repeat the cluster and the source.
const backupDestName = (dest) => {
  const parts = (dest || '').split('/').filter(Boolean)
  return parts.length > 1 ? `…/${parts[parts.length - 1]}` : dest
}

// verificationLabel describes a backup's last verification (#2023) for its status tooltip.
const verificationLabel = (v) => {
  if (!v) return 'Not verified'
  const when = v.endTime && !v.endTime.startsWith('0001') ? ` · ${formatDate(v.endTime)}` : ''
  const notes = v.notes?.length ? ` · ${v.notes.join(' · ')}` : ''
  switch (v.status) {
    case 'running':
      return `Verification (${v.level}) running`
    case 'skipped':
      return `Verification (${v.level}) not run${when}: ${v.error}`
    case 'passed':
      return v.warnings?.length
        ? `Verified (${v.level})${when}, but the backup lacks objects its source has: ${v.warnings.join(' · ')}${notes}`
        : `Verified (${v.level})${when}${notes}`
    default:
      return `Verification (${v.level}) failed${when}: ${v.error}`
  }
}

// timeSourceLabel says where a backup's time comes from (#2073): its own record, its file's
// time (when it was placed, not dumped), or the operator.
const timeSourceLabel = (source) => {
  switch (source) {
    case 'dump-footer':
      return 'taken: the dump footer'
    case 'mydumper-metadata':
      return 'taken: mydumper metadata'
    case 'file-time':
      return 'file time: when the file was placed, not when it was dumped'
    case 'operator':
      return 'taken: set by the operator'
    default:
      return ''
  }
}

// sameArtifact compares two fingerprints of a backup's file (#2023, #2073).
const sameArtifact = (a, b) => !!a && !!b && a.size === b.size && a.files === b.files && a.modTime === b.modTime && (a.inode || 0) === (b.inode || 0)

// verificationBeforeChange: the verification of a discovered backup read its file before the
// file changed, or before the change was registered.
const verificationBeforeChange = (backup, change) => {
  const v = backup.verification
  if (!backup.discovered || !v?.artifact) return false
  return !!change || (backup.registration && !sameArtifact(v.artifact, backup.registration.artifact))
}

// registeredVerificationFailed: the last verification of a registered backup failed on its
// current file, ended after the file was registered (#2108).
const registeredVerificationFailed = (backup) => {
  const v = backup.verification
  return !!backup.discovered && !!backup.registration && v?.status === 'failed' && new Date(v.endTime) >= new Date(backup.registration.registeredAt)
}

// registeredProven: the last verification of a registered backup is a passed test restore of
// its current file (#2108).
const registeredProven = (backup) => {
  const v = backup.verification
  return !!backup.discovered && !!backup.registration && v?.level === 'restore' && v?.status === 'passed' && sameArtifact(v.artifact, backup.registration.artifact)
}

// reseedRefusal is why a backup can not reseed a server, '' when it can.
const reseedRefusal = (backup, change, requireProven) => {
  if (backup.backupMethod === 2 || !['mysqldump', 'mydumper'].includes(backup.backupTool)) return 'A reseed from a chosen backup takes a logical backup'
  if (backup.notABackup) return `Not a backup: ${backup.discoveryNote}`
  if (change) return 'Changed since registered: register the change first'
  if (!backup.completed) return 'Not completed'
  if (backup.originalRemoved) return 'Kept only in its archive: restore it from its snapshot'
  if (!backup.binLogFileName && !backup.binLogUuid) return 'No replication position: a replica can not be reseeded from it'
  if (registeredVerificationFailed(backup)) return `Its last verification failed (WARN0253): verify it again, register a fixed file or unregister it`
  if (requireProven && backup.discovered && !registeredProven(backup)) return 'A registered backup is reseeded from once a test restore of its file passed (backup-verification-registered-proven)'
  return ''
}

// archiveRefusal is why a registered backup can not be archived now, '' when it can (#2108);
// the API decides, this greys the button out. archiveOn: an archive is attached (#2091), or
// backup-restic is on. Already archived: its snapshot is in the attached archive (recorded by
// archive name; resticSnapshotID before the archive list), as the API checks.
const archivedIn = (backup, attached) => {
  const refs = backup.archiveRefs || {}
  return Object.keys(refs).length === 0 ? !!backup.resticSnapshotID : !!refs[attached]
}
const archiveRefusal = (backup, change, archiveOn, attached) => {
  if (!archiveOn) return 'No archive: attach one in the Archive settings'
  if (archivedIn(backup, attached) || backup.originalRemoved) return 'Already archived'
  if (backup.notABackup) return `Not a backup: ${backup.discoveryNote}`
  if (change) return 'Changed since registered: register the change first'
  if (backup.verification?.status === 'running') return 'Being verified'
  return ''
}

const candidateStatusLabel = {
  backup: 'Backup',
  'not-backup': 'Not a backup',
  incomplete: 'Incomplete',
  unchecked: 'Not checked',
  unreadable: 'Unreadable',
  settling: 'Being copied?'
}

// section: undefined = full page, 'backup' = backup accordions only, 'jobs' = jobs accordion only
function Maintenance({ selectedCluster, user, section, onOpenBackupSettings, onOpenArchiveSettings, onOpenSchedulerSettings, onOpenLogsSettings }) {
  const [data, setData] = useState([])
  const [snapshotData, setSnapshotData] = useState([])
  const [queueData, setQueueData] = useState([])
  const [confirmState, setConfirmState] = useState({ isOpen: false, title: '', payload: null })
  // BKU plan (prov-db-bku): the per-cluster backup storage reservation, a PLAN not a resource,
  // so it lives with the backups, not among the configurator's resource gauges. The measured
  // side (backupUnits, every 30 ticks) is shown next to it; over the plan = billed.
  const [bkuConfirm, setBkuConfirm] = useState({ isOpen: false, title: '', delta: 0 })
  // GWU plan (prov-gateway-units): the per-cluster egress reservation through the Cloud18
  // gateways (#1872); the reading (gatewayUnits, month to date) is shown next to it.
  const [gwuConfirm, setGwuConfirm] = useState({ isOpen: false, title: '', delta: 0 })
  const gwu = selectedCluster?.gatewayUnits
  const gwuPlan = selectedCluster?.config?.provGatewayUnits ?? 0
  const gwuUnit = gwu?.unitMbit || 100
  const clusterData = useSelector((state) => state.cluster?.clusterData)
  const bkuGB = getUnitRatios(clusterData).storage.diskGBPerUnit || 20
  const bku = selectedCluster?.backupUnits
  const bkuPlan = parseInt(selectedCluster?.config?.provDbBku) || 0
  const { isOpen: isConfirmModalOpen, title, payload } = confirmState
  // reseed of a server from a chosen backup: the backup, then the server picked for it
  const [reseedPick, setReseedPick] = useState({ backup: null, serverId: '' })
  const clusterServers = useSelector((state) => state.cluster.clusterServers)
  const reseedTargets = (clusterServers || [])
    .filter((s) => s.state !== 'Master')
    .map((s) => ({ name: `${s.url} (${s.state})`, value: s.id }))
  const closeReseedPick = () => setReseedPick({ backup: null, serverId: '' })

  const dispatch = useDispatch()
  const columnHelper = createColumnHelper()
  const { isOpen: isBackupsOpen, onToggle: onBackupsToggle } = useDisclosure({
    defaultIsOpen: JSON.parse(localStorage.getItem('isBackupsOpen')) === false ? false : true
  })
  const { isOpen: isBackupSnapshotOpen, onToggle: onBackupSnapshotToggle } = useDisclosure({
    defaultIsOpen: JSON.parse(localStorage.getItem('isBackupSnapshotOpen')) || false
  })
  const { isOpen: isDBJobsOpen, onToggle: onDBJobsToggle } = useDisclosure({
    defaultIsOpen: JSON.parse(localStorage.getItem('isDBJobsOpen')) || false
  })
  const { isOpen: isLogsOpen, onToggle: onLogsToggle } = useDisclosure({
    defaultIsOpen: JSON.parse(localStorage.getItem('isLogsInBackupOpen')) || false
  })

  const list = useSelector((state) => state.cluster.backups.list)
  // what the backup discovery found (#2073): candidates per server, registered backups changed since
  const discovery = useSelector((state) => state.cluster.backups.discovery)
  const changesById = useMemo(() => {
    const out = {}
    for (const c of discovery?.changes || []) out[c.id] = c
    return out
  }, [discovery])
  const candidateData = useMemo(() => (discovery?.servers || []).flatMap((s) => s.candidates || []), [discovery])
  const droppedCandidates = (discovery?.servers || []).filter((s) => s.dropped > 0)
  const { isOpen: isCandidatesOpen, onToggle: onCandidatesToggle } = useDisclosure({
    defaultIsOpen: JSON.parse(localStorage.getItem('isBackupCandidatesOpen')) === false ? false : true
  })
  // Register of a candidate or of a registered backup's change: the target, and the time of
  // a backup that records none (optional)
  const [registerPick, setRegisterPick] = useState({ candidate: null, backup: null, takenAt: '' })
  const closeRegisterPick = () => setRegisterPick({ candidate: null, backup: null, takenAt: '' })
  // Archive of a registered backup (#2108): its two opt-ins, off by default
  const [archivePick, setArchivePick] = useState({ backup: null, removeOriginal: false, verifyRestore: false })
  const closeArchivePick = () => setArchivePick({ backup: null, removeOriginal: false, verifyRestore: false })
  const backupStats = useSelector((state) => state.cluster.backups.stats)

  const snapshots = useSelector((state) => state.cluster.restic.snapshots)
  const stats = useSelector((state) => state.cluster.restic.stats)
  const resticQueue = useSelector((state) => state.cluster.restic.queue)
  const currentResticTask = useSelector((state) => state.cluster.restic.currentTask)
  const resticRepoPath = useSelector((state) => state.cluster.restic.repoPath)
  // the attached archive (#2091): the restic repository, snapshots and queue are a restic archive's
  const [attachedArchive, setAttachedArchive] = useState(null)
  const [snapbackAsk, setSnapbackAsk] = useState(null) // a restic snapshot asked for a snapback: { id, path }
  // server URL -> the log copies of its backup directory (#2091): number, lowest, highest
  const [binlogCopies, setBinlogCopies] = useState({})
  const backupCount = sizeOf(list || {})
  useEffect(() => {
    if (!selectedCluster?.name) return
    clusterService.getBinlogCopies(selectedCluster.name).then(({ data, status }) => {
      if (status >= 200 && status < 300 && Array.isArray(data)) setBinlogCopies(Object.fromEntries(data.map((c) => [c.server, c])))
    }).catch(() => {})
  }, [selectedCluster?.name, backupCount])
  const resticShown = !attachedArchive || attachedArchive.plugin?.startsWith('archive-restic-')
  // where an archive of a backup goes: the attached archive by name, else restic
  const archiveTarget = selectedCluster?.config?.backupArchiveName || 'restic'

  const openConfirmModal = (title, payload) => {
    setConfirmState({ isOpen: true, title, payload })
  }

  const closeConfirmModal = () => {
    setConfirmState({ isOpen: false, title: '', payload: null })
  }

  const handleConfirm = () => {
    if (payload && payload.action) {
      switch (payload.action) {
        case 'backupDelete':
          dispatch(deleteBackup({ clusterName: selectedCluster.name, backupId: payload.data.backupId }))
          break
        case 'backupDiscover':
          dispatch(discoverBackups({ clusterName: selectedCluster.name }))
          break
        case 'backupUnregister':
          dispatch(unregisterBackup({ clusterName: selectedCluster.name, backupId: payload.data.backupId }))
          break
        case 'backupVerify':
          dispatch(verifyBackup({ clusterName: selectedCluster.name, backupId: payload.data.backupId, level: payload.data.level }))
          break
        case 'snapshotPurge':
          dispatch(purgeResticSnapshot({ clusterName: selectedCluster.name, snapshotId: payload.data.snapshotId }))
          break
        case 'queueCancel':
          dispatch(resticQueueCancel({ clusterName: selectedCluster.name, taskId: payload.data.taskId }))
          break
        case 'queueMove':
          dispatch(resticQueueMove({ clusterName: selectedCluster.name, taskId: payload.data.taskId, direction: payload.data.direction, afterId: payload.data.afterId }))
          break
        case 'queuePause':
          dispatch(resticQueuePause({ clusterName: selectedCluster.name }));
          break
        case 'queueResume':
          dispatch(resticQueueResume({ clusterName: selectedCluster.name }));
          break
        default:
          dispatch(showWarningToast({ title: 'Unknown action', description: `The action ${payload.action} is not recognized.` }))
          break
      }
    }

    closeConfirmModal()
  }

  const handleMove = (direction, afterId) => {
    setConfirmState((prevState) => ({
      ...prevState,
      payload: {
        ...prevState.payload,
        data: {
          ...prevState.payload.data,
          direction,
          afterId
        }
      }
    }))
  }

  useEffect(() => {
    localStorage.setItem('isBackupSnapshotOpen', JSON.stringify(isBackupSnapshotOpen))
  }, [isBackupSnapshotOpen])
  useEffect(() => {
    localStorage.setItem('isDBJobsOpen', JSON.stringify(isDBJobsOpen))
  }, [isDBJobsOpen])

  useEffect(() => {
    localStorage.setItem('isLogsInBackupOpen', JSON.stringify(isLogsOpen))
  }, [isLogsOpen])
  useEffect(() => {
    localStorage.setItem('isBackupsOpen', JSON.stringify(isBackupsOpen))
  }, [isBackupsOpen])
  useEffect(() => {
    localStorage.setItem('isBackupCandidatesOpen', JSON.stringify(isCandidatesOpen))
  }, [isCandidatesOpen])

  useEffect(() => {
    if (list) {
      const arrData = convertObjectToArray(list).reverse()
      const dataWithColor = arrData.map((item) => {
        const endTime = item.endTime ? new Date(item.endTime) : null
        const hasEnded = endTime && !isNaN(endTime.getTime()) && endTime.getFullYear() > 1
        return {
          ...item,
          rowColor: !item.completed && hasEnded ? 'red' : ''
        }
      })
      setData(dataWithColor)
    } else {
      setData([])
    }
  }, [selectedCluster?.name, list])

  useEffect(() => {
    if (snapshots?.length > 0) {
      setSnapshotData(snapshots)
    } else {
      setSnapshotData([])
    }
  }, [selectedCluster?.name, snapshots])

  useEffect(() => {
    if (resticQueue?.length > 0) {
      const arrData = convertObjectToArray(resticQueue)
      setQueueData(arrData.reverse())
    } else {
      setQueueData([])
    }
  }, [selectedCluster?.name, resticQueue])

  const backupSlotsTotal = selectedCluster?.backupSlotsTotal || 0
  const backupSlotsInUse = selectedCluster?.backupSlotsInUse || 0

  const backupDataStats = [
    {
      key: 'Total Size',
      value: sizeOf(backupStats?.total_size)
    },
    {
      key: 'Total File Count',
      value: backupStats?.total_file_count
    },
    {
      key: 'Total Blob Count',
      value: backupStats?.total_blob_count
    },
    {
      key: 'Free Backup Slots',
      value: backupSlotsTotal > 0 ? backupSlotsTotal - backupSlotsInUse : 'Unlimited'
    }
  ]

  const columns = useMemo(
    () => [
      columnHelper.accessor(
        (row) => (
          <>
            {formatDate(row.startTime)}
            {row.timeSource && <Box fontSize='xs' opacity={0.7}>{timeSourceLabel(row.timeSource)}</Box>}
          </>
        ),
        {
          cell: (info) => info.getValue(),
          header: 'Started',
          id: 'startTime',
          minWidth: 150
        }
      ),
      columnHelper.accessor((row) => backupDuration(row), {
        cell: (info) => info.getValue(),
        header: 'Duration',
        id: 'duration'
      }),
      columnHelper.accessor(
        (row) => (
          <VStack className={styles.cellStack} spacing={0.5}>
            <Box className={styles.cellValue}>{row.source}</Box>
            <BackupCellLine label='File:' value={row.dest} shown={backupDestName(row.dest)} textType='Destination' />
          </VStack>
        ),
        {
          cell: (info) => info.getValue(),
          header: 'Server',
          id: 'source'
        }
      ),
      columnHelper.accessor((row) => formatBytes(row.size), {
        cell: (info) => info.getValue(),
        header: 'Size',
        id: 'backupSize',
        minWidth: 100
      }),
      columnHelper.accessor(
        (row) => <BackupTags tags={backupTags(row, changesById[row.id], selectedCluster?.config?.backupArchiveName)} />,
        {
          cell: (info) => info.getValue(),
          header: 'Tags',
          id: 'tags',
          minWidth: 260
        }
      ),
      columnHelper.accessor(
        (row) => (
          <VStack className={styles.cellStack} spacing={0.5}>
            <Box className={styles.cellValue}>{`File: ${row.binLogFileName} / Pos: ${row.binLogFilePos}`}</Box>
            <BackupGtid gtid={row.binLogUuid} />
            <BackupLogsLine copies={binlogCopies[row.source]} />
          </VStack>
        ),
        {
          cell: (info) => info.getValue(),
          header: 'BCK Position',
          id: 'binLogInfo'
        }
      ),
      columnHelper.display({
        id: 'actions',
        header: 'Actions',
        cell: (info) => {
          const backup = info.row.original
          const change = changesById[backup.id]
          const verifiable = backup.backupTool === 'mysqldump' && backup.completed && !backup.notABackup && !backup.originalRemoved && backup.verification?.status !== 'running'
          const notReseedable = reseedRefusal(backup, change, selectedCluster?.config?.backupVerificationRegisteredProven)
          const notArchivable = backup.discovered ? archiveRefusal(backup, change, !!selectedCluster?.config?.backupArchiveName || selectedCluster?.config?.backupRestic, selectedCluster?.config?.backupArchiveName) : ''
          const noBackupGrant = !user?.grants['db-backup'] && 'You do not have the db-backup grant'
          const notVerifiable = !verifiable && 'Only a completed mysqldump backup whose file is here can be verified'
          return (
            <MenuOptions
              placement='left-end'
              options={[
                {
                  name: 'Verify integrity',
                  onClick: () => openConfirmModal('Verify the integrity of this backup?', { action: 'backupVerify', data: { backupId: backup.id, level: 'integrity' } }),
                  isDisabled: !!(noBackupGrant || notVerifiable),
                  disabledReason: noBackupGrant || notVerifiable
                },
                {
                  name: selectedCluster?.config?.backupVerificationHost ? `Test restore on ${selectedCluster.config.backupVerificationHost}` : 'Test restore',
                  onClick: () => openConfirmModal(`Load this backup into ${selectedCluster?.config?.backupVerificationHost} and compare it?`, { action: 'backupVerify', data: { backupId: backup.id, level: 'restore' } }),
                  isDisabled: !!(noBackupGrant || notVerifiable || !selectedCluster?.config?.backupVerificationHost),
                  disabledReason: noBackupGrant || notVerifiable || 'Set backup-verification-host first'
                },
                {
                  name: 'Reseed a server from this backup',
                  onClick: () => setReseedPick({ backup, serverId: '' }),
                  isDisabled: !user?.grants['db-restore'] || !!notReseedable,
                  disabledReason: !user?.grants['db-restore'] ? 'You do not have the db-restore grant' : notReseedable
                },
                ...(change
                  ? [
                      {
                        name: 'Register the change',
                        onClick: () => setRegisterPick({ candidate: null, backup, takenAt: '' }),
                        isDisabled: !!noBackupGrant,
                        disabledReason: noBackupGrant
                      }
                    ]
                  : []),
                ...(backup.discovered
                  ? [
                      {
                        name: `Archive to ${archiveTarget}`,
                        onClick: () => setArchivePick({ backup, removeOriginal: false, verifyRestore: false }),
                        isDisabled: !!(noBackupGrant || notArchivable),
                        disabledReason: noBackupGrant || notArchivable
                      },
                      {
                        name: 'Unregister',
                        onClick: () => openConfirmModal(`Unregister backup ${backup.id}? Its file is left as it is and listed in "Found in backup folders" again.`, { action: 'backupUnregister', data: { backupId: backup.id } }),
                        isDisabled: !!noBackupGrant,
                        disabledReason: noBackupGrant
                      }
                    ]
                  : [
                      {
                        name: 'Delete backup',
                        onClick: () => openConfirmModal('Do you want to delete this backup?', { action: 'backupDelete', data: { backupId: backup.id } }),
                        isDisabled: !!noBackupGrant,
                        disabledReason: noBackupGrant
                      }
                    ])
              ]}
            />
          )
        }
      })
    ]
  )

  // the candidates of the backup discovery (#2073): what was found in the backup folders, the
  // files that are not backups included, with why; a backup is registered on request only
  const candidateColumns = useMemo(
    () => [
      columnHelper.accessor((row) => row.server, { header: 'Server', id: 'server' }),
      columnHelper.accessor(
        (row) => <BackupCellLine label='' value={row.path} shown={row.name} textType='Path' />,
        { cell: (info) => info.getValue(), header: 'Found', id: 'path' }
      ),
      columnHelper.accessor((row) => (row.splitDump ? 'splitdump' : row.tool), { header: 'Tool', id: 'tool' }),
      columnHelper.accessor((row) => formatBytes(row.size), { header: 'Size', id: 'size' }),
      columnHelper.accessor((row) => formatDate(row.modTime), { header: 'File time', id: 'modTime' }),
      columnHelper.accessor(
        (row) => (
          <VStack className={styles.cellStack} spacing={0.5} align='start'>
            <Box className={styles.cellValue} fontWeight='semibold'>{candidateStatusLabel[row.status] || row.status}</Box>
            {row.reason && <Box className={styles.cellValue} fontSize='xs'>{row.reason}</Box>}
            {row.registering && <Box className={styles.cellValue} fontSize='xs'>Registering: being read…</Box>}
            {row.registerError && <Box className={styles.cellValue} fontSize='xs' color='red.500'>Register refused: {row.registerError}</Box>}
          </VStack>
        ),
        { cell: (info) => info.getValue(), header: 'Check', id: 'status' }
      ),
      columnHelper.display({
        id: 'actions',
        header: 'Actions',
        cell: (info) => {
          const c = info.row.original
          const registerable = ['backup', 'unchecked', 'unreadable'].includes(c.status) && !c.registering
          return (
            <RMIconButton
              icon={HiDocumentAdd}
              tooltip={registerable ? 'Register: it is read to its end, registered only if it is a proper backup' : `Not registerable: ${c.reason || candidateStatusLabel[c.status]}`}
              onClick={() => setRegisterPick({ candidate: c, backup: null, takenAt: '' })}
              isDisabled={!user?.grants['db-backup'] || !registerable}
            />
          )
        }
      })
    ]
  )

  const snapshotDataStats = [
    {
      key: 'Total Size',
      value: sizeOf(stats?.total_size)
    },
    {
      key: 'Total File Count',
      value: stats?.total_file_count
    },
    {
      key: 'Total Blob Count',
      value: stats?.total_blob_count
    }
  ]

  const snapshotColumns = useMemo(() => [
    columnHelper.accessor((row) => row.short_id, {
      header: 'ID',
      id: 'id'
    }),
    columnHelper.accessor((row) => row.time, {
      header: 'Time'
    }),
    columnHelper.accessor((row) => row.paths?.join(','), {
      header: 'Path'
    }),
    columnHelper.accessor((row) => row.hostname, {
      header: 'Hostname'
    }),
    columnHelper.accessor((row) => row.tags?.join(','), {
      header: 'Tags'
    }),
    // Added Purge action column
    columnHelper.display({
      id: 'actions',
      header: 'Actions',
      cell: (info) => (
        <>
        <RMIconButton icon={HiRewind} tooltip='Snapback: put the backup directory back as it is in this snapshot'
          isDisabled={user?.grants?.['db-restore'] === false}
          onClick={() => setSnapbackAsk({ id: info.row.original.id, path: info.row.original.paths?.[0] || '' })} />
        <RMIconButton
          icon={HiTrash}
          tooltip='Purge snapshot'
          onClick={() => openConfirmModal('Do you want to purge this snapshot?', { action: 'snapshotPurge', data: { snapshotId: info.row.original.id } })}
          isDisabled={!user?.grants['cluster-process']}
        />
        </>
      )
    })
  ])

  const queuelength = queueData?.length || 0;

  const queueDataHeader = useMemo(() =>[
    {
      key: 'Total Pending Tasks',
      value: queuelength
    },
    {
      key: 'Queue Status',
      value: selectedCluster?.isResticQueuePaused ? 'Paused' : 'Running'
    },
    {
      key: 'Action',
      value: (selectedCluster?.isResticQueuePaused ?
        <RMIconButton icon={HiPlay} tooltip='Resume queue' isDisabled={!user?.grants['cluster-process']} onClick={() => openConfirmModal('Resume Restic Queue', { action: 'queueResume' })} /> :
        <RMIconButton icon={HiPause} tooltip='Pause queue' isDisabled={!user?.grants['cluster-process']} onClick={() => openConfirmModal('Pause Restic Queue', { action: 'queuePause' })} />
      )
    }
  ], [selectedCluster?.isResticQueuePaused, queuelength])

  const currentTaskHeader = useMemo(() => {
    if (!currentResticTask) {
      return []
    }

    const isCopyTask = currentResticTask.task_type === 8
    const isBackupTask = currentResticTask.task_type === 2
    const phase = currentResticTask.phase

    const percentDone = (() => {
      const hasPercent = typeof currentResticTask.percent_done === 'number'

      if (phase === 'init_destination') {
        return (
          <HStack spacing={3} alignItems="center">
            <Progress size="sm" flex="1" isIndeterminate />
            <Box minWidth="3.5rem" textAlign="right">Preparing...</Box>
          </HStack>
        )
      }

      if ((isBackupTask || (isCopyTask && phase === 'copy')) && hasPercent) {
        const clamped = Math.min(Math.max(currentResticTask.percent_done, 0), 1)
        const percentValue = Math.round(clamped * 100)
        return (
          <HStack spacing={3} alignItems="center">
            <Progress value={percentValue} size="sm" flex="1" />
            <Box minWidth="3.5rem" textAlign="right">{percentValue}%</Box>
          </HStack>
        )
      }

      return 'Running'
    })()
    const bytesValue = currentResticTask.total_bytes
      ? `${formatBytes(currentResticTask.bytes_done || 0)} / ${formatBytes(currentResticTask.total_bytes)}`
      : '-'
    const filesValue = currentResticTask.total_files
      ? `${currentResticTask.files_done || 0} / ${currentResticTask.total_files}`
      : '-'
    const packsValue = currentResticTask.total_packs
      ? `${currentResticTask.packs_done || 0} / ${currentResticTask.total_packs}`
      : '-'
    const startedAt = currentResticTask.started_at ? formatDate(currentResticTask.started_at) : '-'
    const completedAt = currentResticTask.completed_at ? formatDate(currentResticTask.completed_at) : '-'
    const duration = currentResticTask.total_duration
      ? `${Math.round(currentResticTask.total_duration)}s`
      : currentResticTask.seconds_elapsed
        ? `${currentResticTask.seconds_elapsed}s`
        : '-'

    return [
      { key: 'Task ID', value: currentResticTask.task_id || '-' },
      { key: 'Task Type', value: resticTaskType(currentResticTask.task_type) },
      { key: 'Status', value: currentResticTask.status || '-' },
      ...(currentResticTask.phase ? [{ key: 'Phase', value: currentResticTask.phase }] : []),
      { key: 'Progress', value: percentDone },
      { key: 'Bytes', value: bytesValue },
      ...(isCopyTask
        ? [{ key: 'Packs', value: packsValue }]
        : [{ key: 'Files', value: filesValue }]),
      ...(isCopyTask && currentResticTask.total_snapshots > 0 ? [{
        key: 'Snapshots',
        value: `${currentResticTask.completed_snapshots || 0} / ${currentResticTask.total_snapshots}`
      }] : []),
      { key: 'Snapshot ID', value: currentResticTask.snapshot_id || '-' },
      { key: 'Duration', value: duration },
      { key: 'Started', value: startedAt },
      { key: 'Completed', value: completedAt }
    ]
  }, [currentResticTask])

  const queueColumns = useMemo(() => [
    columnHelper.accessor((row) => row.task_id, {
      header: 'ID',
      id: 'task_id'
    }),
    columnHelper.accessor((row) => resticTaskType(row.task_type), {
      header: 'Task Type'
    }),
    columnHelper.accessor((row) => resticTaskDetail(row), {
      header: 'Details',
      cell: (info) => info.getValue(),
      id: 'details',
      minWidth: 200
    }),
    // Added cancel action column
    columnHelper.display({
      id: 'actions',
      header: 'Actions',
      cell: (info) => (
        <RMIconButton
          icon={HiTrash}
          tooltip='Cancel queued task'
          onClick={() => openConfirmModal('Cancel Queued Task', { action: 'queueCancel', data: { taskId: info.row.original.task_id } })}
          isDisabled={!user?.grants['cluster-process']}
        />
      )
    })
  ])

  const settingsButton = (onClick, tooltip) =>
    onClick ? (
      <RMIconButton
        icon={HiCog}
        tooltip={tooltip}
        onClick={onClick}
        size='xs'
        variant='ghost'
      />
    ) : null

  const backupSection = (
    <>
      <AccordionComponent
        heading={'Current Backups'}
        isOpen={isBackupsOpen}
        onToggle={onBackupsToggle}
        className={styles.accordion}
        headerClassName={styles.accordionHeader}
        panelClassName={styles.accordionPanel}
        headerActions={
          <HStack spacing={1}>
            {settingsButton(onOpenBackupSettings, 'Open Backup Settings')}
          </HStack>
        }
        body={
          <VStack className={styles.snapshotContainer}>
            <BKUSlider
              isDisabled={user?.grants['cluster-settings'] == false}
              value={bkuPlan || 1}
              unitGB={bkuGB}
              bku={bku}
              onChange={(value) => {
                const delta = value - bkuPlan
                if (delta === 0) return
                setBkuConfirm({ isOpen: true, delta, title: `Confirm the backup plan at ${value} BKU = ${value * bkuGB} GB of local backup storage for the cluster` })
              }}
            />
            <TableType3 dataArray={backupDataStats} className={styles.statsTable} />
            {gwu && (<Flex gap={3} alignItems='center' wrap='wrap'>
              <Text fontWeight='bold'>Gateway network plan (GWU, {gwuUnit} Mb/s each)</Text>
              <TextForm
                value={gwuPlan > 0 ? String(gwuPlan) : ''}
                type='number'
                placeholder={gwu ? `follows the gateway: ${gwu.plan.toFixed(2)} GWU` : 'follows the gateway'}
                confirmTitle='Confirm the gateway network plan in GWU for the cluster: '
                onSave={(value) => {
                  const next = parseInt(value, 10)
                  if (!Number.isFinite(next) || next < 1) return
                  const delta = next - gwuPlan
                  if (delta === 0) return
                  setGwuConfirm({ isOpen: true, delta, title: `Confirm the gateway network plan at ${next} GWU = ${next * gwuMB} MB exchanged per month through the gateways` })
                }}
              />
              <Text>{gwu ? `plan ${gwu.plan.toFixed(2)} GWU = ${gwu.planMbps.toFixed(0)} Mb/s${gwu.pinned ? ' (pinned)' : ' (gateway capacity / clusters present)'}, consumed ${gwu.units.toFixed(3)} GWU = ${gwu.mbps.toFixed(1)} Mb/s, ${gwu.units > gwu.plan ? `borrowing ${(gwu.units - gwu.plan).toFixed(2)}` : `giving away ${(gwu.plan - gwu.units).toFixed(2)}`} GWU; ${gwu.freeUnits} GWU free, ${gwu.onTop.toFixed(3)} GWU held above it now, ${gwu.onTopGbit.toFixed(3)} Gbit moved above it this month (reported to the back office); ${gwu.gbit.toFixed(2)} Gbit moved in total on ${gwu.gateways} gateway(s)` : 'no reading yet'}</Text>
            </Flex>)}
            <DataTable key="backups" data={data} columns={columns} className={styles.table} />
          </VStack>
        }
      />
      <AccordionComponent
        heading={`Found in backup folders${candidateData.length ? ` (${candidateData.length})` : ''}`}
        isOpen={isCandidatesOpen}
        onToggle={onCandidatesToggle}
        className={styles.accordion}
        headerClassName={styles.accordionHeader}
        panelClassName={styles.accordionPanel}
        headerActions={
          <RMIconButton
            icon={HiSearch}
            tooltip='Discover: scan the backup folders for the backups no metadata describes'
            onClick={() => openConfirmModal('Scan the backup folders for the backups no metadata describes? Nothing is registered: register the backups you want used.', { action: 'backupDiscover' })}
            isDisabled={!user?.grants['db-backup']}
            size='xs'
            variant='ghost'
          />
        }
        body={
          <VStack className={styles.snapshotContainer}>
            <Text fontSize='sm'>
              Backups placed in the backup folders by hand or by another tool are listed here with what their check found; they are used only once registered, and never by an automatic restore.
            </Text>
            {droppedCandidates.map((s) => (
              <Text key={s.server} fontSize='sm' color='orange.500'>
                {s.server}: {s.dropped} more not listed, beyond backup-discovery-candidate-max (WARN0250)
              </Text>
            ))}
            <DataTable key="candidates" data={candidateData} columns={candidateColumns} className={styles.table} />
          </VStack>
        }
      />
      <AccordionComponent
        heading={'Archive'}
        isOpen={isBackupSnapshotOpen}
        onToggle={onBackupSnapshotToggle}
        className={styles.accordion}
        headerClassName={styles.accordionHeader}
        panelClassName={styles.accordionPanel}
        headerActions={settingsButton(onOpenArchiveSettings || onOpenBackupSettings, 'Open Archive Settings')}
        body={
          <VStack className={styles.snapshotContainer}>
            <AttachedArchive clusterName={selectedCluster?.name} user={user} onAttached={setAttachedArchive}
              snapbackAsk={snapbackAsk} onSnapbackAskDone={() => setSnapbackAsk(null)} />
            {resticShown && (
              <>
                {/* an attached restic archive's location is in the row above */}
                {!attachedArchive && (
                  <Box className={styles.repoRow}>
                    <Box className={styles.repoRowLabel}>Restic repository:</Box>
                    <Box className={styles.repoRowValue}>{resticRepoPath || '-'}</Box>
                  </Box>
                )}
                <TableType3 dataArray={snapshotDataStats} className={styles.statsTable} />
                <DataTable key="snapshot" data={snapshotData} columns={snapshotColumns} className={styles.table} />
                <Box className={styles.sectionTitle}>Current Restic Task</Box>
                {currentResticTask ? (
                  <TableType3 dataArray={currentTaskHeader} className={`${styles.statsTable} ${styles.currentTaskTable}`} />
                ) : (
                  <Box className={styles.emptyState}>No active task</Box>
                )}
                <TableType3 dataArray={queueDataHeader} className={styles.statsTable} />
                <DataTable key="queue" data={queueData} columns={queueColumns} className={styles.table} />
              </>
            )}
          </VStack>
        }
      />
    </>
  )

  const jobsSection = (
    <AccordionComponent
      heading={'Database Jobs'}
      isOpen={isDBJobsOpen}
      onToggle={onDBJobsToggle}
      className={styles.accordion}
      headerClassName={styles.accordionHeader}
      panelClassName={styles.accordionPanel}
      headerActions={settingsButton(onOpenSchedulerSettings, 'Open Scheduler Settings')}
      body={<DatabaseJobs clusterName={selectedCluster?.name} user={user} />}
    />
  )

  const logsSection = (
    <AccordionComponent
      className={styles.accordion}
      isOpen={isLogsOpen}
      onToggle={onLogsToggle}
      headerClassName={styles.accordionHeader}
      panelClassName={styles.accordionPanel}
      heading={'Job Logs'}
      headerActions={settingsButton(onOpenLogsSettings, 'Open Log Settings')}
      body={<TaskLogs />}
    />
  )

  return (
    <VStack className={styles.backupContainer}>
      {(!section || section === 'backup') && backupSection}
      {(!section || section === 'jobs') && jobsSection}
      {!section && logsSection}
      {gwuConfirm.isOpen && <ConfirmModal title={gwuConfirm.title} isOpen={gwuConfirm.isOpen}
        onConfirmClick={() => { dispatch(changePlanUnits({ clusterName: selectedCluster?.name, unit: 'GWU', delta: gwuConfirm.delta })); setGwuConfirm({ isOpen: false, title: '', delta: 0 }) }}
        closeModal={() => setGwuConfirm({ isOpen: false, title: '', delta: 0 })} />}
      {bkuConfirm.isOpen && <ConfirmModal title={bkuConfirm.title} isOpen={bkuConfirm.isOpen}
        onConfirmClick={() => { dispatch(changePlanUnits({ clusterName: selectedCluster?.name, unit: 'BKU', delta: bkuConfirm.delta })); setBkuConfirm({ isOpen: false, title: '', delta: 0 }) }}
        closeModal={() => setBkuConfirm({ isOpen: false, title: '', delta: 0 })} />}
      {reseedPick.backup && <ConfirmModal title={`Reseed a server from backup ${reseedPick.backup.id}?`} isOpen={!!reseedPick.backup}
        body={
          <VStack align='stretch' spacing={3}>
            <Text>
              The server's data is replaced by the backup ({reseedPick.backup.dest}), then its replication restarts from the
              position the backup holds.{reseedPick.backup.discovered ? ' This backup was discovered in a backup folder: check it is the one you mean.' : ''}
            </Text>
            <Dropdown options={reseedTargets} placeholder='Server to reseed' selectedValue={reseedPick.serverId}
              onChange={(option) => setReseedPick((p) => ({ ...p, serverId: option.value }))} />
          </VStack>
        }
        onConfirmClick={() => {
          if (!reseedPick.serverId) {
            dispatch(showWarningToast({ title: 'Pick the server to reseed' }))
            return
          }
          dispatch(reseedFromBackup({ clusterName: selectedCluster.name, serverId: reseedPick.serverId, backupId: reseedPick.backup.id }))
          closeReseedPick()
        }}
        closeModal={closeReseedPick} />}
      {(registerPick.candidate || registerPick.backup) && <ConfirmModal
        title={registerPick.candidate ? `Register ${registerPick.candidate.name}?` : `Register the change of backup ${registerPick.backup.id}?`}
        isOpen={!!(registerPick.candidate || registerPick.backup)}
        body={
          <VStack align='stretch' spacing={3}>
            <Text>
              {registerPick.candidate
                ? 'It is read to its end as a restore would read it and registered only if it is a proper backup: it holds a table and is complete. Its file is not touched.'
                : `${changesById[registerPick.backup.id]?.describe || 'Its file changed'}. The file as it is now is read and checked as a new backup; once registered, it is used. If the change was not intended, restore the original from its archive or unregister it instead.`}
            </Text>
            <Text fontSize='sm'>Taken at (only for a backup that records no time of its own, such as a splitdump; otherwise leave empty):</Text>
            <Input type='datetime-local' value={registerPick.takenAt} onChange={(e) => setRegisterPick((p) => ({ ...p, takenAt: e.target.value }))} />
          </VStack>
        }
        onConfirmClick={() => {
          const takenAt = registerPick.takenAt ? new Date(registerPick.takenAt).toISOString() : ''
          if (registerPick.candidate) {
            dispatch(registerBackup({ clusterName: selectedCluster.name, server: registerPick.candidate.server, path: registerPick.candidate.path, takenAt }))
          } else {
            dispatch(registerBackupChange({ clusterName: selectedCluster.name, backupId: registerPick.backup.id, takenAt }))
          }
          closeRegisterPick()
        }}
        closeModal={closeRegisterPick} />}
      {archivePick.backup && <ConfirmModal
        title={`Archive backup ${archivePick.backup.id} to ${archiveTarget}?`}
        isOpen={!!archivePick.backup}
        body={
          <VStack align='stretch' spacing={3}>
            <Text>
              Its file ({archivePick.backup.dest}) is pushed to {archiveTarget}, after the pushes already queued; its
              snapshot is recorded in the backup.
            </Text>
            <Checkbox isChecked={archivePick.removeOriginal}
              onChange={(e) => setArchivePick((p) => ({ ...p, removeOriginal: e.target.checked }))}>
              Delete the backup file from the backup folder once {archiveTarget} holds it
            </Checkbox>
            {selectedCluster?.config?.backupVerificationHost && archivePick.backup.backupTool === 'mysqldump' && (
              <Checkbox isChecked={archivePick.verifyRestore} isDisabled={!user?.grants['db-restore']}
                onChange={(e) => setArchivePick((p) => ({ ...p, verifyRestore: e.target.checked }))}>
                {`Verify by test restore on ${selectedCluster.config.backupVerificationHost} first: archived only if it passes`}
              </Checkbox>
            )}
          </VStack>
        }
        onConfirmClick={() => {
          dispatch(archiveBackup({ clusterName: selectedCluster.name, backupId: archivePick.backup.id, removeOriginal: archivePick.removeOriginal, verifyRestore: archivePick.verifyRestore }))
          closeArchivePick()
        }}
        closeModal={closeArchivePick} />}
      {isConfirmModalOpen && <ConfirmModal title={title} isOpen={isConfirmModalOpen} body={<DynamicForm
        payload={payload}
        queueData={queueData}
        selectedCluster={selectedCluster}
        handleMove={handleMove}
      />} onConfirmClick={handleConfirm} closeModal={closeConfirmModal} />}
    </VStack>
  )
}

export default Maintenance
