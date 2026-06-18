import { Box, Checkbox, Flex, Grid, GridItem, HStack, Text, VStack } from '@chakra-ui/react'
import React, { useState } from 'react'
import styles from './styles.module.scss'
import NumberInput from '../../../components/NumberInput'
import TextForm from '../../../components/TextForm'
import { useDispatch } from 'react-redux'
import { setSetting } from '../../../redux/settingsSlice'
import RMIconButton from '../../../components/RMIconButton'
import { HiChevronDown, HiChevronUp, HiQuestionMarkCircle, HiTrash } from 'react-icons/hi'
import CommonModal from '../../../components/Modals/CommonModal'
import ConfirmModal from '../../../components/Modals/ConfirmModal'
import modalStyles from '../../../components/Modals/styles.module.scss'
import Markdown from 'react-markdown'
import remarkGfm from 'remark-gfm'
import { purgeResticByPolicy } from '../../../redux/clusterSlice'
import RMSwitch from '../../../components/RMSwitch'

function ResticPurgeStrategy({ clusterName, config }) {
  const dispatch = useDispatch()

  const joinClasses = (...classes) => classes.filter(Boolean).join(' ')

  const [isCommonModalOpen, setIsCommonModalOpen] = useState(false)
  const [isPurgeModalOpen, setIsPurgeModalOpen] = useState(false)
  const createPurgeChecklist = () => ({
    acknowledgePolicy: false,
    acknowledgeImpact: false,
    dryRun: false
  })
  const [purgeChecklist, setPurgeChecklist] = useState(createPurgeChecklist)
  const [action, setAction] = useState({
    title: '',
    body: <></>
  })
  const { title, body } = action
  const [openPanels, setOpenPanels] = useState({
    grouping: false,
    filters: false,
    pruneOptions: false,
    retention: true,
    preview: false,
    purge: false
  })

  const ResticKeepLastNTooltip = `
Keep the most recent N snapshots for this bucket.  
Example: 3 keeps the latest 3 snapshots.  
Set to 0 to disable this rule.  
Combined with keep-within: a snapshot is retained if it matches either rule.`

  const ResticKeepWithinTooltip = `
Keep snapshots within a recent time window for this bucket.  
Examples: "2h", "1d", "1d2h".  
Units: h (hours), d (days), m (months), y (years).  
Leave blank to disable this rule.  
Combined with keep-last: a snapshot is retained if it matches either rule.  
Window is counted back from when the purge runs.  
`

  const columnTitles = {
    keepLast: 'Keep Last N',
    keepWithin: 'Keep Within Duration'
  }

  const ResticPurgeGroupByTooltip = `
Controls how snapshots are grouped before retention is applied.  
Retention runs per group (not across groups).  
Use "default" for restic defaults or "none" for a single global group.  
host: group by client hostname.  
paths: group by source paths.  
tags: group by tag set.  
Examples: "host", "paths", "host,paths", "tags".  
Leave blank to use restic defaults.`

  const ResticKeepTagTooltip = `
Keep-tag overrides selection by always retaining snapshots that match the tag.  
Retention runs normally; any snapshot with a keep-tag survives forget.  
Provide space-separated tags, e.g. "line:adhoc env:prod".  
Commas inside a tag mean AND in restic (use quotes if needed).  
You can use {cluster} or {tenant} placeholders (example: "cluster:{cluster}").  
Wrap a literal tag in quotes to prevent placeholder processing.  
Leave empty to disable.
`

  const ResticPurgeFilterTooltip = `
Filters limit which snapshots are eligible for forget.  
All filter types are AND (host + tag + path).  
Multiple values inside one filter are OR (any match).  
Host/path filters accept comma or space separated values.  
Tag filters are space separated; commas inside a tag mean AND.  
Paths must be absolute (e.g. /var/lib/mysql).  
Leave empty to match all.  
`

  const ResticPurgeSelectionTooltip = `
How selection works: filters limit eligible snapshots, then group-by splits them into groups, and retention runs per group.  
Keep-tag always wins by retaining matching snapshots even if they would be removed.  
Command preview reflects the current selection and retention settings.
`

  const ResticPurgeBehaviorTooltip = `
Prune runs after forget to reclaim repository space.  
It can be slower but reduces storage usage.  
Dry-run only shows what would be removed.  
`

  const ResticPurgePruneTuningTooltip = `
Tuning affects compaction/repack behavior.  
Only applies when prune is enabled.  
Use conservative values unless you know the repo layout.  
`

  const ResticPurgePruneTooltip = `${ResticPurgeBehaviorTooltip}${ResticPurgePruneTuningTooltip}`

  const ResticPurgeFilterHostHelp = `**Filter Host**

Limits forget/prune selection to snapshots created on the listed host(s).
Accepts a comma- or space-separated list; multiple values are OR'd together.
Combined with Filter Tag and Filter Path using AND.

Maps to one or more restic \`--host\` flags.

Config: \`backup-restic-purge-host\``

  const ResticPurgeFilterTagHelp = `**Filter Tag**

Limits forget/prune selection to snapshots matching the given tag(s).
Space-separated; commas inside a tag mean AND (quote tags containing commas).
Combined with Filter Host and Filter Path using AND.

Maps to one or more restic \`--tag\` flags.

Config: \`backup-restic-purge-tag\``

  const ResticPurgeFilterPathHelp = `**Filter Path**

Limits forget/prune selection to snapshots that include the given absolute path(s).
Accepts a comma- or space-separated list; multiple values are OR'd together.
Combined with Filter Host and Filter Tag using AND.

Maps to one or more restic \`--path\` flags.

Config: \`backup-restic-purge-path\``

  const ResticPurgePruneSwitchHelp = `**Prune**

When enabled, \`restic prune\` runs after \`forget\` to reclaim space freed by removed snapshots.
Pruning can take significantly longer and use more I/O/CPU than forget alone.

Maps to the restic \`--prune\` flag.

Config: \`backup-restic-purge-prune\``

  const ResticPurgePruneCompactHelp = `**Prune Compact**

When enabled (and Prune is on), pack files are rewritten even if they would only shrink slightly, keeping the repository more compact at the cost of extra I/O.

Maps to the restic \`--compact\` flag (prune only).

Config: \`backup-restic-purge-prune-compact\``

  const ResticPurgePruneMaxUnusedHelp = `**Max Unused**

Caps the amount of unused (no longer referenced) data restic tolerates in the repository after pruning, as an absolute size (e.g. \`5G\`) or percentage (e.g. \`10%\`). \`unlimited\` disables the limit.

Maps to the restic \`--max-unused\` flag (prune only). Leave empty to use restic's default.

Config: \`backup-restic-purge-prune-max-unused\``

  const ResticPurgePruneMaxRepackSizeHelp = `**Max Repack Size**

Caps the total size of pack files that may be repacked during a single prune run (e.g. \`5G\`). Useful to bound how long/expensive a prune operation can get.

Maps to the restic \`--max-repack-size\` flag (prune only). Leave empty for no limit.

Config: \`backup-restic-purge-prune-max-repack-size\``

  const ResticPurgePruneRepackCacheableOnlyHelp = `**Repack Cacheable Only**

When enabled (and Prune is on), only repacks pack files containing cacheable data (metadata), skipping data blobs. Speeds up pruning at the cost of reclaiming less space.

Maps to the restic \`--repack-cacheable-only\` flag (prune only).

Config: \`backup-restic-purge-prune-repack-cacheable-only\``

  const ResticPurgePruneRepackSmallHelp = `**Repack Small**

When enabled (and Prune is on), small pack files are repacked together to reduce the total number of files in the repository.

Maps to the restic \`--repack-small\` flag (prune only).

Config: \`backup-restic-purge-prune-repack-small\``

  const ResticPurgePruneRepackUncompressedHelp = `**Repack Uncompressed**

When enabled (and Prune is on), pack files written in restic's older uncompressed format are repacked into the compressed format, reducing repository size over time.

Maps to the restic \`--repack-uncompressed\` flag (prune only).

Config: \`backup-restic-purge-prune-repack-uncompressed\``

  const togglePanel = (panelKey) => {
    setOpenPanels((prev) => ({
      ...prev,
      [panelKey]: !prev[panelKey]
    }))
  }

  const setAllPanels = (isOpen) => {
    setOpenPanels((prev) =>
      Object.keys(prev).reduce((acc, key) => {
        acc[key] = isOpen
        return acc
      }, {})
    )
  }

  const renderPanel = (panelKey, titleContent, content) => (
    <Box className={styles.panel} w="full">
      <HStack
        as="button"
        type="button"
        spacing={2}
        onClick={() => togglePanel(panelKey)}
        aria-expanded={openPanels[panelKey]}
        aria-controls={`restic-panel-${panelKey}`}
        className={styles.panelHeader}
      >
        {typeof titleContent === 'string' ? (
          <Text className={styles.panelTitle}>{titleContent}</Text>
        ) : (
          titleContent
        )}
        <Box className={styles.panelChevron}>
          {openPanels[panelKey] ? <HiChevronUp /> : <HiChevronDown />}
        </Box>
      </HStack>
      <Box
        id={`restic-panel-${panelKey}`}
        className={styles.panelBody}
        display={openPanels[panelKey] ? 'block' : 'none'}
      >
        {content}
      </Box>
    </Box>
  )

  const openCommonModal = () => {
    setIsCommonModalOpen(true)
  }

  const closeCommonModal = () => {
    setIsCommonModalOpen(false)
  }

  const openPurgeModal = () => {
    setPurgeChecklist(createPurgeChecklist())
    setIsPurgeModalOpen(true)
  }

  const closePurgeModal = () => {
    setIsPurgeModalOpen(false)
  }

  const openInfoModal = (modalTitle, tooltip) => {
    setAction({
      title: modalTitle,
      body: (
        <Box className={joinClasses(modalStyles.infoTooltip, styles.infoTooltip)}>
          <Markdown remarkPlugins={[remarkGfm]}>{tooltip}</Markdown>
        </Box>
      )
    })
    openCommonModal()
  }

  const h = (content, title) => (
    <RMIconButton
      icon={HiQuestionMarkCircle}
      iconFontsize='1rem'
      variant='ghost'
      style={{ opacity: 0.5, minWidth: '1.5rem', height: '1.5rem' }}
      onClick={(event) => {
        event?.stopPropagation?.()
        openInfoModal(title, content)
      }}
    />
  )

  const toInt = (value) => {
    const parsed = Number(value)
    return Number.isFinite(parsed) ? parsed : 0
  }

  const splitKeepTagTemplates = (value) => {
    const parts = []
    let current = ''
    let quote = null
    let escaped = false

    for (let i = 0; i < value.length; i += 1) {
      const ch = value[i]
      if (quote) {
        if (quote === '"' && !escaped && ch === '\\') {
          escaped = true
          current += ch
          continue
        }
        if (quote === '"' && escaped) {
          current += ch
          escaped = false
          continue
        }
        if (ch === quote) {
          quote = null
        }
        current += ch
        continue
      }

      if (ch === '"' || ch === "'") {
        quote = ch
        current += ch
        continue
      }

      if (ch === ',' || ch === ' ' || ch === '\t' || ch === '\n' || ch === '\r') {
        if (current.trim()) {
          parts.push(current)
        }
        current = ''
        continue
      }

      current += ch
    }

    if (current.trim()) {
      parts.push(current)
    }

    return parts
  }

  const splitTagFilterValues = (value) => {
    const parts = []
    let current = ''
    let quote = null
    let escaped = false

    for (let i = 0; i < value.length; i += 1) {
      const ch = value[i]
      if (quote) {
        if (quote === '"' && !escaped && ch === '\\') {
          escaped = true
          current += ch
          continue
        }
        if (quote === '"' && escaped) {
          current += ch
          escaped = false
          continue
        }
        if (ch === quote) {
          quote = null
        }
        current += ch
        continue
      }

      if (ch === '"' || ch === "'") {
        quote = ch
        current += ch
        continue
      }

      if (ch === ' ' || ch === '\t' || ch === '\n' || ch === '\r') {
        if (current.trim()) {
          parts.push(current)
        }
        current = ''
        continue
      }

      current += ch
    }

    if (current.trim()) {
      parts.push(current)
    }

    return parts
  }

  const unquoteKeepTagLiteral = (value) => {
    if (value.length < 2) {
      return value
    }
    const first = value[0]
    const last = value[value.length - 1]
    if ((first === '"' && last === '"') || (first === "'" && last === "'")) {
      return value.slice(1, -1)
    }
    return value
  }

  const splitListValues = (value) => {
    if (!value) {
      return []
    }
    return value
      .split(/[\s,]+/)
      .map((entry) => entry.trim())
      .filter(Boolean)
  }

  const buildForgetCommand = () => {
    const args = ['restic', 'forget']
    const pruneEnabled = Boolean(config?.backupResticPurgePrune)
    if (pruneEnabled) {
      args.push('--prune')
    }
    const groupBy = config?.backupResticPurgeGroupBy?.trim()
    if (groupBy && groupBy.toLowerCase() !== 'default') {
      if (groupBy.toLowerCase() === 'none') {
        args.push('--group-by', "''")
      } else {
        args.push('--group-by', groupBy)
      }
    }

    splitListValues(config?.backupResticPurgeHost).forEach((host) => {
      args.push('--host', host)
    })
    const purgeTagValue = config?.backupResticPurgeTag || ''
    splitTagFilterValues(purgeTagValue)
      .map((tag) => unquoteKeepTagLiteral(tag.trim()))
      .filter(Boolean)
      .forEach((tag) => {
        args.push('--tag', tag)
      })
    splitListValues(config?.backupResticPurgePath).forEach((path) => {
      args.push('--path', path)
    })

    const keepWithinMap = [
      ['--keep-within', config?.backupKeepWithin],
      ['--keep-within-hourly', config?.backupKeepWithinHourly],
      ['--keep-within-daily', config?.backupKeepWithinDaily],
      ['--keep-within-weekly', config?.backupKeepWithinWeekly],
      ['--keep-within-monthly', config?.backupKeepWithinMonthly],
      ['--keep-within-yearly', config?.backupKeepWithinYearly]
    ]

    keepWithinMap.forEach(([flag, value]) => {
      if (value) {
        args.push(flag, value)
      }
    })

    const keepMap = [
      ['--keep-last', toInt(config?.backupKeepLast)],
      ['--keep-hourly', toInt(config?.backupKeepHourly)],
      ['--keep-daily', toInt(config?.backupKeepDaily)],
      ['--keep-weekly', toInt(config?.backupKeepWeekly)],
      ['--keep-monthly', toInt(config?.backupKeepMonthly)],
      ['--keep-yearly', toInt(config?.backupKeepYearly)]
    ]

    keepMap.forEach(([flag, value]) => {
      if (value > 0) {
        args.push(flag, String(value))
      }
    })

    const keepTagValue = config?.backupResticPurgeKeepTag || ''
    splitKeepTagTemplates(keepTagValue)
      .map((tag) => unquoteKeepTagLiteral(tag.trim()))
      .filter(Boolean)
      .forEach((tag) => {
        args.push('--keep-tag', tag)
      })

    if (pruneEnabled) {
      if (config?.backupResticPurgePruneCompact) {
        args.push('--compact')
      }
      const maxUnused = config?.backupResticPurgePruneMaxUnused?.trim()
      if (maxUnused) {
        args.push('--max-unused', maxUnused)
      }
      const maxRepackSize = config?.backupResticPurgePruneMaxRepackSize?.trim()
      if (maxRepackSize) {
        args.push('--max-repack-size', maxRepackSize)
      }
      if (config?.backupResticPurgePruneRepackCacheableOnly) {
        args.push('--repack-cacheable-only')
      }
      if (config?.backupResticPurgePruneRepackSmall) {
        args.push('--repack-small')
      }
      if (config?.backupResticPurgePruneRepackUncompressed) {
        args.push('--repack-uncompressed')
      }
    }

    return args.join(' ')
  }

  const handleSave = (key, value) => {
    dispatch(setSetting({
      clusterName: clusterName,
      setting: key,
      value
    }))
  }

  const purgeChecklistItems = [
    {
      key: 'acknowledgePolicy',
      label: 'I reviewed the command preview and retention filters (required).',
      required: true
    },
    {
      key: 'acknowledgeImpact',
      label: 'I understand this purge permanently removes snapshots not retained by policy (required).',
      required: true
    },
    {
      key: 'dryRun',
      label: 'Dry run only (adds dry_run=1 and does not delete data).',
      required: false
    }
  ]

  const isPurgeConfirmEnabled = purgeChecklistItems
    .filter((item) => item.required)
    .every((item) => purgeChecklist[item.key])

  const handlePurgeConfirm = () => {
    dispatch(purgeResticByPolicy({ clusterName, dryRun: purgeChecklist.dryRun }))
    closePurgeModal()
  }

  const ResticKeepRecentHelp = `**Keep Recent**

Baseline retention rule applied on top of the hourly/daily/weekly/monthly/yearly buckets below.

- Keep the **N** most recent snapshots, regardless of when they were created.
- Keep snapshots created **within** a recent time window (e.g. \`2d\`, \`1d2h\`).

A snapshot is retained if it matches either rule. Set Keep Last N to 0 and leave Keep Within blank to disable this rule.

Config: \`backup-keep-last\` / \`backup-keep-within\``

  const ResticKeepHourlyHelp = `**Keep Hourly**

Retains one snapshot per hour for the most recent hours, in addition to any other matching rules.

- Keep the **N** most recent hourly snapshots.
- Keep snapshots created **within** a recent time window (e.g. \`2h\`, \`1d\`) for this bucket.

Set Keep Last N to 0 and leave Keep Within blank to disable hourly retention.

Config: \`backup-keep-hourly\` / \`backup-keep-within-hourly\``

  const ResticKeepDailyHelp = `**Keep Daily**

Retains one snapshot per day for the most recent days, in addition to any other matching rules.

- Keep the **N** most recent daily snapshots.
- Keep snapshots created **within** a recent time window (e.g. \`7d\`) for this bucket.

Set Keep Last N to 0 and leave Keep Within blank to disable daily retention.

Config: \`backup-keep-daily\` / \`backup-keep-within-daily\``

  const ResticKeepWeeklyHelp = `**Keep Weekly**

Retains one snapshot per week for the most recent weeks, in addition to any other matching rules.

- Keep the **N** most recent weekly snapshots.
- Keep snapshots created **within** a recent time window (e.g. \`28d\`) for this bucket.

Set Keep Last N to 0 and leave Keep Within blank to disable weekly retention.

Config: \`backup-keep-weekly\` / \`backup-keep-within-weekly\``

  const ResticKeepMonthlyHelp = `**Keep Monthly**

Retains one snapshot per month for the most recent months, in addition to any other matching rules.

- Keep the **N** most recent monthly snapshots.
- Keep snapshots created **within** a recent time window (e.g. \`6m\`) for this bucket.

Set Keep Last N to 0 and leave Keep Within blank to disable monthly retention.

Config: \`backup-keep-monthly\` / \`backup-keep-within-monthly\``

  const ResticKeepYearlyHelp = `**Keep Yearly**

Retains one snapshot per year for the most recent years, in addition to any other matching rules.

- Keep the **N** most recent yearly snapshots.
- Keep snapshots created **within** a recent time window (e.g. \`5y\`) for this bucket.

Set Keep Last N to 0 and leave Keep Within blank to disable yearly retention.

Config: \`backup-keep-yearly\` / \`backup-keep-within-yearly\``

  const sections = [
    {
      title: "Keep Recent",
      help: ResticKeepRecentHelp,
      colA: (
        <NumberInput containerClassName={styles.marginCenter} value={config?.backupKeepLast} confirmTitle={"Confirm update 'backup-keep-last':"} min={0} showEditButton={true} showConfirmModal={true} onConfirm={(v) => { handleSave('backup-keep-last', v) }} />
      ),
      colB: (
        <TextForm className={styles.marginCenter} size="sm" value={config?.backupKeepWithin} confirmTitle={"Confirm update 'backup-keep-within':"} regexPattern="^(\d+y)?(\d+m)?(\d+d)?(\d+h)?$" onSave={(v) => { handleSave('backup-keep-within', v) }} />
      )
    },
    {
      title: "Keep Hourly",
      help: ResticKeepHourlyHelp,
      colA: (
        <NumberInput containerClassName={styles.marginCenter} value={config?.backupKeepHourly} confirmTitle={"Confirm update 'backup-keep-hourly':"} min={0} showEditButton={true} showConfirmModal={true} onConfirm={(v) => { handleSave('backup-keep-hourly', v) }} />
      ),
      colB: (
        <TextForm className={styles.marginCenter} size="sm" value={config?.backupKeepWithinHourly} confirmTitle={"Confirm update 'backup-keep-within-hourly':"} regexPattern="^(\d+y)?(\d+m)?(\d+d)?(\d+h)?$" onSave={(v) => { handleSave('backup-keep-within-hourly', v) }} />
      )
    },
    {
      title: "Keep Daily",
      help: ResticKeepDailyHelp,
      colA: (
        <NumberInput containerClassName={styles.marginCenter} value={config?.backupKeepDaily} confirmTitle={"Confirm update 'backup-keep-daily':"} min={0} showEditButton={true} showConfirmModal={true} onConfirm={(v) => { handleSave('backup-keep-daily', v) }} />
      ),
      colB: (
        <TextForm className={styles.marginCenter} size="sm" value={config?.backupKeepWithinDaily} confirmTitle={"Confirm update 'backup-keep-within-daily':"} regexPattern="^(\d+y)?(\d+m)?(\d+d)?(\d+h)?$" onSave={(v) => { handleSave('backup-keep-within-daily', v) }} />
      )
    },
    {
      title: "Keep Weekly",
      help: ResticKeepWeeklyHelp,
      colA: (
        <NumberInput containerClassName={styles.marginCenter} value={config?.backupKeepWeekly} confirmTitle={"Confirm update 'backup-keep-weekly':"} min={0} showEditButton={true} showConfirmModal={true} onConfirm={(v) => { handleSave('backup-keep-weekly', v) }} />
      ),
      colB: (
        <TextForm className={styles.marginCenter} size="sm" value={config?.backupKeepWithinWeekly} confirmTitle={"Confirm update 'backup-keep-within-weekly':"} regexPattern="^(\d+y)?(\d+m)?(\d+d)?(\d+h)?$" onSave={(v) => { handleSave('backup-keep-within-weekly', v) }} />
      )
    },
    {
      title: "Keep Monthly",
      help: ResticKeepMonthlyHelp,
      colA: (
        <NumberInput containerClassName={styles.marginCenter} value={config?.backupKeepMonthly} confirmTitle={"Confirm update 'backup-keep-monthly':"} min={0} showEditButton={true} showConfirmModal={true} onConfirm={(v) => { handleSave('backup-keep-monthly', v) }} />
      ),
      colB: (
        <TextForm className={styles.marginCenter} size="sm" value={config?.backupKeepWithinMonthly} confirmTitle={"Confirm update 'backup-keep-within-monthly':"} regexPattern="^(\d+y)?(\d+m)?(\d+d)?(\d+h)?$" onSave={(v) => { handleSave('backup-keep-within-monthly', v) }} />
      )
    },
    {
      title: "Keep Yearly",
      help: ResticKeepYearlyHelp,
      colA: (
        <NumberInput containerClassName={styles.marginCenter} value={config?.backupKeepYearly} confirmTitle={"Confirm update 'backup-keep-yearly':"} min={0} showEditButton={true} showConfirmModal={true} onConfirm={(v) => { handleSave('backup-keep-yearly', v) }} />
      ),
      colB: (
        <TextForm className={styles.marginCenter} size="sm" value={config?.backupKeepWithinYearly} confirmTitle={"Confirm update 'backup-keep-within-yearly':"} regexPattern="^(\d+y)?(\d+m)?(\d+d)?(\d+h)?$" onSave={(v) => { handleSave('backup-keep-within-yearly', v) }} />
      )
    }
  ];

  return (
    <VStack spacing={3} align="stretch" w={"100%"}>
      <Flex
        className={styles.purgeSelectionRow}
        direction={{ base: 'column', md: 'row' }}
        align={{ base: 'flex-start', md: 'center' }}
        justify="space-between"
        gap={2}
      >
        <HStack spacing={2} className={styles.purgeSelectionInfo}>
          <Text className={styles.purgeSelectionTitle}>Purge selection</Text>
          {h(ResticPurgeSelectionTooltip, 'Purge Selection')}
        </HStack>
        <HStack
          spacing={2}
          className={styles.purgeSelectionActions}
          w={{ base: 'full', md: 'auto' }}
          justify={{ base: 'flex-start', md: 'flex-end' }}
          flexWrap="wrap"
        >
          <Box
            as="button"
            type="button"
            className={styles.panelActionButton}
            onClick={() => setAllPanels(true)}
          >
            Show all
          </Box>
          <Box
            as="button"
            type="button"
            className={styles.panelActionButton}
            onClick={() => setAllPanels(false)}
          >
            Hide all
          </Box>
        </HStack>
      </Flex>
      {renderPanel(
        'grouping',
        'Grouping & Tags',
        (
          <Grid
            className={styles.filterGrid}
            templateColumns={{ base: '1fr', md: 'minmax(160px, 0.7fr) minmax(240px, 1fr)' }}
            columnGap={3}
            rowGap={2}
            w="full"
          >
            <GridItem className={styles.rowLabel}>
              <HStack spacing={2}>
                <Text>Group By</Text>
                {h(ResticPurgeGroupByTooltip, 'Restic Group By')}
              </HStack>
            </GridItem>
            <GridItem className={styles.valueCell}>
              <TextForm
                size="sm"
                value={config?.backupResticPurgeGroupBy}
                confirmTitle={"Confirm update 'backup-restic-purge-group-by':"}
                onSave={(v) => { handleSave('backup-restic-purge-group-by', v) }}
              />
            </GridItem>
            <GridItem className={styles.rowLabel}>
              <HStack spacing={2}>
                <Text>Keep Tag</Text>
                {h(ResticKeepTagTooltip, 'Restic Keep Tag')}
              </HStack>
            </GridItem>
            <GridItem className={styles.valueCell}>
              <TextForm
                size="sm"
                value={config?.backupResticPurgeKeepTag}
                confirmTitle={"Confirm update 'backup-restic-purge-keep-tag':"}
                onSave={(v) => { handleSave('backup-restic-purge-keep-tag', v) }}
              />
            </GridItem>
          </Grid>
        )
      )}
      {renderPanel(
        'filters',
        (
          <HStack spacing={2} className={styles.panelTitleRow}>
            <Text className={styles.panelTitle}>Filters</Text>
            {h(ResticPurgeFilterTooltip, 'Restic Purge Filters')}
          </HStack>
        ),
        (
          <Grid
            className={styles.filterGrid}
            templateColumns={{ base: '1fr', md: 'minmax(160px, 0.7fr) minmax(240px, 1fr)' }}
            columnGap={3}
            rowGap={2}
            w="full"
          >
            <GridItem className={styles.rowLabel}>
              <HStack spacing={2} justify='space-between' width='full'>
                <Text>Filter Host</Text>
                {h(ResticPurgeFilterHostHelp, 'Filter Host')}
              </HStack>
            </GridItem>
            <GridItem className={styles.valueCell}>
              <TextForm
                size="sm"
                value={config?.backupResticPurgeHost}
                confirmTitle={"Confirm update 'backup-restic-purge-host':"}
                onSave={(v) => { handleSave('backup-restic-purge-host', v) }}
              />
            </GridItem>
            <GridItem className={styles.rowLabel}>
              <HStack spacing={2} justify='space-between' width='full'>
                <Text>Filter Tag</Text>
                {h(ResticPurgeFilterTagHelp, 'Filter Tag')}
              </HStack>
            </GridItem>
            <GridItem className={styles.valueCell}>
              <TextForm
                size="sm"
                value={config?.backupResticPurgeTag}
                confirmTitle={"Confirm update 'backup-restic-purge-tag':"}
                onSave={(v) => { handleSave('backup-restic-purge-tag', v) }}
              />
            </GridItem>
            <GridItem className={styles.rowLabel}>
              <HStack spacing={2} justify='space-between' width='full'>
                <Text>Filter Path</Text>
                {h(ResticPurgeFilterPathHelp, 'Filter Path')}
              </HStack>
            </GridItem>
            <GridItem className={styles.valueCell}>
              <TextForm
                size="sm"
                value={config?.backupResticPurgePath}
                confirmTitle={"Confirm update 'backup-restic-purge-path':"}
                onSave={(v) => { handleSave('backup-restic-purge-path', v) }}
              />
            </GridItem>
          </Grid>
        )
      )}
      {renderPanel(
        'pruneOptions',
        (
          <HStack spacing={2} className={styles.panelTitleRow}>
            <Text className={styles.panelTitle}>Prune</Text>
            {h(ResticPurgePruneTooltip, 'Restic Prune Options')}
          </HStack>
        ),
        (
          <Grid
            className={styles.filterGrid}
            templateColumns={{ base: '1fr', md: 'minmax(160px, 0.7fr) minmax(240px, 1fr)' }}
            columnGap={3}
            rowGap={2}
            w="full"
          >
            <GridItem className={styles.rowLabel}>
              <HStack spacing={2} justify='space-between' width='full'>
                <Text>Prune</Text>
                {h(ResticPurgePruneSwitchHelp, 'Prune')}
              </HStack>
            </GridItem>
            <GridItem className={styles.valueCell}>
              <RMSwitch
                isChecked={config?.backupResticPurgePrune}
                confirmTitle={'Confirm switch settings for backup-restic-purge-prune?'}
                onChange={() => dispatch(setSetting({
                  clusterName: clusterName,
                  setting: 'backup-restic-purge-prune',
                  value: !config?.backupResticPurgePrune
                }))}
              />
            </GridItem>
            <GridItem className={styles.rowLabel}>
              <HStack spacing={2} justify='space-between' width='full'>
                <Text>Prune Compact</Text>
                {h(ResticPurgePruneCompactHelp, 'Prune Compact')}
              </HStack>
            </GridItem>
            <GridItem className={styles.valueCell}>
              <RMSwitch
                isChecked={config?.backupResticPurgePruneCompact}
                isDisabled={!config?.backupResticPurgePrune}
                confirmTitle={'Confirm switch settings for backup-restic-purge-prune-compact?'}
                onChange={() => dispatch(setSetting({
                  clusterName: clusterName,
                  setting: 'backup-restic-purge-prune-compact',
                  value: !config?.backupResticPurgePruneCompact
                }))}
              />
            </GridItem>
            <GridItem className={styles.rowLabel}>
              <HStack spacing={2} justify='space-between' width='full'>
                <Text>Max Unused</Text>
                {h(ResticPurgePruneMaxUnusedHelp, 'Max Unused')}
              </HStack>
            </GridItem>
            <GridItem className={styles.valueCell}>
              <TextForm
                size="sm"
                value={config?.backupResticPurgePruneMaxUnused}
                confirmTitle={"Confirm update 'backup-restic-purge-prune-max-unused':"}
                onSave={(v) => { handleSave('backup-restic-purge-prune-max-unused', v) }}
                isDisabled={!config?.backupResticPurgePrune}
              />
            </GridItem>
            <GridItem className={styles.rowLabel}>
              <HStack spacing={2} justify='space-between' width='full'>
                <Text>Max Repack Size</Text>
                {h(ResticPurgePruneMaxRepackSizeHelp, 'Max Repack Size')}
              </HStack>
            </GridItem>
            <GridItem className={styles.valueCell}>
              <TextForm
                size="sm"
                value={config?.backupResticPurgePruneMaxRepackSize}
                confirmTitle={"Confirm update 'backup-restic-purge-prune-max-repack-size':"}
                onSave={(v) => { handleSave('backup-restic-purge-prune-max-repack-size', v) }}
                isDisabled={!config?.backupResticPurgePrune}
              />
            </GridItem>
            <GridItem className={styles.rowLabel}>
              <HStack spacing={2} justify='space-between' width='full'>
                <Text>Repack Cacheable Only</Text>
                {h(ResticPurgePruneRepackCacheableOnlyHelp, 'Repack Cacheable Only')}
              </HStack>
            </GridItem>
            <GridItem className={styles.valueCell}>
              <RMSwitch
                isChecked={config?.backupResticPurgePruneRepackCacheableOnly}
                isDisabled={!config?.backupResticPurgePrune}
                confirmTitle={'Confirm switch settings for backup-restic-purge-prune-repack-cacheable-only?'}
                onChange={() => dispatch(setSetting({
                  clusterName: clusterName,
                  setting: 'backup-restic-purge-prune-repack-cacheable-only',
                  value: !config?.backupResticPurgePruneRepackCacheableOnly
                }))}
              />
            </GridItem>
            <GridItem className={styles.rowLabel}>
              <HStack spacing={2} justify='space-between' width='full'>
                <Text>Repack Small</Text>
                {h(ResticPurgePruneRepackSmallHelp, 'Repack Small')}
              </HStack>
            </GridItem>
            <GridItem className={styles.valueCell}>
              <RMSwitch
                isChecked={config?.backupResticPurgePruneRepackSmall}
                isDisabled={!config?.backupResticPurgePrune}
                confirmTitle={'Confirm switch settings for backup-restic-purge-prune-repack-small?'}
                onChange={() => dispatch(setSetting({
                  clusterName: clusterName,
                  setting: 'backup-restic-purge-prune-repack-small',
                  value: !config?.backupResticPurgePruneRepackSmall
                }))}
              />
            </GridItem>
            <GridItem className={styles.rowLabel}>
              <HStack spacing={2} justify='space-between' width='full'>
                <Text>Repack Uncompressed</Text>
                {h(ResticPurgePruneRepackUncompressedHelp, 'Repack Uncompressed')}
              </HStack>
            </GridItem>
            <GridItem className={styles.valueCell}>
              <RMSwitch
                isChecked={config?.backupResticPurgePruneRepackUncompressed}
                isDisabled={!config?.backupResticPurgePrune}
                confirmTitle={'Confirm switch settings for backup-restic-purge-prune-repack-uncompressed?'}
                onChange={() => dispatch(setSetting({
                  clusterName: clusterName,
                  setting: 'backup-restic-purge-prune-repack-uncompressed',
                  value: !config?.backupResticPurgePruneRepackUncompressed
                }))}
              />
            </GridItem>
          </Grid>
        )
      )}
      {renderPanel(
        'retention',
        'Retention Policy',
        (
          <Grid
            className={`${styles.container}`}
            templateColumns={{ base: '1fr', md: 'minmax(140px, 0.7fr) minmax(220px, 1fr) minmax(240px, 1fr)' }}
            columnGap={3}
            rowGap={2}
            w="full"
          >
            <GridItem className={styles.headerCell} display={{ base: 'none', md: 'flex' }} />
            <GridItem className={styles.headerCell} display={{ base: 'none', md: 'flex' }}>
              <Text className={styles.headerText}>{columnTitles.keepLast}</Text>
              {h(ResticKeepLastNTooltip, 'Restic Keep Last N')}
            </GridItem>
            <GridItem className={styles.headerCell} display={{ base: 'none', md: 'flex' }}>
              <Text className={styles.headerText}>{columnTitles.keepWithin}</Text>
              {h(ResticKeepWithinTooltip, 'Restic Keep Within Duration')}
            </GridItem>

            {sections.map((section, index) => (
              <React.Fragment key={index}>
                <GridItem className={styles.rowLabel}>
                  <HStack spacing={2} justify='space-between' width='full'>
                    <Text>{section.title}</Text>
                    {h(section.help, section.title)}
                  </HStack>
                </GridItem>
                <GridItem className={styles.valueCell}>
                  <Text className={styles.mobileHeader} display={{ base: 'block', md: 'none' }}>
                    {columnTitles.keepLast}
                  </Text>
                  {section.colA}
                </GridItem>
                <GridItem className={styles.valueCell}>
                  <Text className={styles.mobileHeader} display={{ base: 'block', md: 'none' }}>
                    {columnTitles.keepWithin}
                  </Text>
                  {section.colB}
                </GridItem>
              </React.Fragment>
            ))}
          </Grid>
        )
      )}
      {renderPanel(
        'preview',
        'Command Preview',
        (
          <Box className={styles.commandBox} borderWidth="1px" borderRadius="md">
            <Text className={styles.commandLabel}>Command preview</Text>
            <Text className={styles.commandText}>{buildForgetCommand()}</Text>
            <Text className={styles.commandHint}>Updates as you change the policy.</Text>
          </Box>
        )
      )}
      {renderPanel(
        'purge',
        'Run Purge',
        (
          <Box className={styles.infoBox} p={2} borderWidth="1px" borderRadius="md" bg="gray.50">
            <RMIconButton icon={HiTrash} onClick={openPurgeModal} />
            <Text as="span">Use the trash icon to run a purge with the current retention policy.</Text>
          </Box>
        )
      )}

      {isCommonModalOpen && (
        <CommonModal
          isOpen={isCommonModalOpen}
          size='lg'
          title={title}
          body={body}
          contentClassName={joinClasses(modalStyles.infoModalContent, styles.infoModalContent)}
          headerClassName={joinClasses(modalStyles.infoModalHeader, styles.infoModalHeader)}
          bodyClassName={joinClasses(modalStyles.infoModalBody, styles.infoModalBody)}
          closeModal={() => {
            closeCommonModal()
          }}
        />
      )}
      {isPurgeModalOpen && (
        <ConfirmModal
          isOpen={isPurgeModalOpen}
          closeModal={closePurgeModal}
          title="Confirm restic purge"
          confirmButtonText={purgeChecklist.dryRun ? 'Run dry run' : 'Run purge'}
          confirmButtonProps={{ isDisabled: !isPurgeConfirmEnabled }}
          onConfirmClick={handlePurgeConfirm}
          body={(
            <Box className={styles.purgeModalBody}>
              <Text className={styles.purgeModalIntro}>
                This runs restic forget/prune using the current purge policy for the repository.
              </Text>
              <VStack align="start" spacing={2} className={styles.purgeChecklist}>
                {purgeChecklistItems.map((item) => (
                  <Checkbox
                    key={item.key}
                    isChecked={purgeChecklist[item.key]}
                    onChange={(event) =>
                      setPurgeChecklist((prev) => ({
                        ...prev,
                        [item.key]: event.target.checked
                      }))
                    }
                    className={styles.purgeChecklistItem}
                  >
                    {item.label}
                  </Checkbox>
                ))}
              </VStack>
              <Text className={styles.purgeModalNote}>
                {purgeChecklist.dryRun
                  ? 'Dry run enabled: no snapshots will be deleted.'
                  : 'Dry run disabled: snapshots outside retention will be removed.'}
              </Text>
            </Box>
          )}
        />
      )}
    </VStack>
  );
}

export default ResticPurgeStrategy
