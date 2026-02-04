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
    grouping: true,
    filters: true,
    pruneOptions: true,
    retention: true,
    preview: true,
    purge: true
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

  const sections = [
    {
      title: "Keep Recent",
      colA: (
        <NumberInput containerClassName={styles.marginCenter} value={config?.backupKeepLast} confirmTitle={"Confirm update 'backup-keep-last':"} min={0} showEditButton={true} showConfirmModal={true} onConfirm={(v) => { handleSave('backup-keep-last', v) }} />
      ),
      colB: (
        <TextForm className={styles.marginCenter} size="sm" value={config?.backupKeepWithin} confirmTitle={"Confirm update 'backup-keep-within':"} regexPattern="^(\d+y)?(\d+m)?(\d+d)?(\d+h)?$" onSave={(v) => { handleSave('backup-keep-within', v) }} />
      )
    },
    {
      title: "Keep Hourly",
      colA: (
        <NumberInput containerClassName={styles.marginCenter} value={config?.backupKeepHourly} confirmTitle={"Confirm update 'backup-keep-hourly':"} min={0} showEditButton={true} showConfirmModal={true} onConfirm={(v) => { handleSave('backup-keep-hourly', v) }} />
      ),
      colB: (
        <TextForm className={styles.marginCenter} size="sm" value={config?.backupKeepWithinHourly} confirmTitle={"Confirm update 'backup-keep-within-hourly':"} regexPattern="^(\d+y)?(\d+m)?(\d+d)?(\d+h)?$" onSave={(v) => { handleSave('backup-keep-within-hourly', v) }} />
      )
    },
    {
      title: "Keep Daily",
      colA: (
        <NumberInput containerClassName={styles.marginCenter} value={config?.backupKeepDaily} confirmTitle={"Confirm update 'backup-keep-daily':"} min={0} showEditButton={true} showConfirmModal={true} onConfirm={(v) => { handleSave('backup-keep-daily', v) }} />
      ),
      colB: (
        <TextForm className={styles.marginCenter} size="sm" value={config?.backupKeepWithinDaily} confirmTitle={"Confirm update 'backup-keep-within-daily':"} regexPattern="^(\d+y)?(\d+m)?(\d+d)?(\d+h)?$" onSave={(v) => { handleSave('backup-keep-within-daily', v) }} />
      )
    },
    {
      title: "Keep Weekly",
      colA: (
        <NumberInput containerClassName={styles.marginCenter} value={config?.backupKeepWeekly} confirmTitle={"Confirm update 'backup-keep-weekly':"} min={0} showEditButton={true} showConfirmModal={true} onConfirm={(v) => { handleSave('backup-keep-weekly', v) }} />
      ),
      colB: (
        <TextForm className={styles.marginCenter} size="sm" value={config?.backupKeepWithinWeekly} confirmTitle={"Confirm update 'backup-keep-within-weekly':"} regexPattern="^(\d+y)?(\d+m)?(\d+d)?(\d+h)?$" onSave={(v) => { handleSave('backup-keep-within-weekly', v) }} />
      )
    },
    {
      title: "Keep Monthly",
      colA: (
        <NumberInput containerClassName={styles.marginCenter} value={config?.backupKeepMonthly} confirmTitle={"Confirm update 'backup-keep-monthly':"} min={0} showEditButton={true} showConfirmModal={true} onConfirm={(v) => { handleSave('backup-keep-monthly', v) }} />
      ),
      colB: (
        <TextForm className={styles.marginCenter} size="sm" value={config?.backupKeepWithinMonthly} confirmTitle={"Confirm update 'backup-keep-within-monthly':"} regexPattern="^(\d+y)?(\d+m)?(\d+d)?(\d+h)?$" onSave={(v) => { handleSave('backup-keep-within-monthly', v) }} />
      )
    },
    {
      title: "Keep Yearly",
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
          <RMIconButton icon={HiQuestionMarkCircle} onClick={() => openInfoModal('Purge Selection', ResticPurgeSelectionTooltip)} />
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
                <RMIconButton icon={HiQuestionMarkCircle} onClick={() => openInfoModal('Restic Group By', ResticPurgeGroupByTooltip)} />
              </HStack>
            </GridItem>
            <GridItem className={styles.valueCell}>
              <TextForm
                size="sm"
                value={config?.backupResticPurgeGroupBy}
                confirmTitle={"Confirm update 'backup-restic-purge-group-by':"}
                onSave={(v) => { handleSave('backup-restic-purge-group-by', v) }}
              />
              <Text className={styles.helperText}>
                Allowed values: host, paths, tags. Comma-separated for multiple.
              </Text>
            </GridItem>
            <GridItem className={styles.rowLabel}>
              <HStack spacing={2}>
                <Text>Keep Tag</Text>
                <RMIconButton icon={HiQuestionMarkCircle} onClick={() => openInfoModal('Restic Keep Tag', ResticKeepTagTooltip)} />
              </HStack>
            </GridItem>
            <GridItem className={styles.valueCell}>
              <TextForm
                size="sm"
                value={config?.backupResticPurgeKeepTag}
                confirmTitle={"Confirm update 'backup-restic-purge-keep-tag':"}
                onSave={(v) => { handleSave('backup-restic-purge-keep-tag', v) }}
              />
              <Text className={styles.helperText}>
                Space-separated tags, e.g. line:adhoc env:prod. Quote tags with commas.
              </Text>
            </GridItem>
          </Grid>
        )
      )}
      {renderPanel(
        'filters',
        (
          <HStack spacing={2} className={styles.panelTitleRow}>
            <Text className={styles.panelTitle}>Filters</Text>
            <RMIconButton
              icon={HiQuestionMarkCircle}
              onClick={(event) => {
                event.stopPropagation()
                openInfoModal('Restic Purge Filters', ResticPurgeFilterTooltip)
              }}
            />
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
              <HStack spacing={2}>
                <Text>Filter Host</Text>
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
              <HStack spacing={2}>
                <Text>Filter Tag</Text>
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
              <HStack spacing={2}>
                <Text>Filter Path</Text>
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
            <RMIconButton
              icon={HiQuestionMarkCircle}
              onClick={(event) => {
                event.stopPropagation()
                openInfoModal('Restic Prune Options', ResticPurgePruneTooltip)
              }}
            />
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
              <HStack spacing={2}>
                <Text>Prune</Text>
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
              <HStack spacing={2}>
                <Text>Prune Compact</Text>
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
              <HStack spacing={2}>
                <Text>Max Unused</Text>
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
              <HStack spacing={2}>
                <Text>Max Repack Size</Text>
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
              <HStack spacing={2}>
                <Text>Repack Cacheable Only</Text>
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
              <HStack spacing={2}>
                <Text>Repack Small</Text>
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
              <HStack spacing={2}>
                <Text>Repack Uncompressed</Text>
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
              <RMIconButton icon={HiQuestionMarkCircle} onClick={() => openInfoModal('Restic Keep Last N', ResticKeepLastNTooltip)} />
            </GridItem>
            <GridItem className={styles.headerCell} display={{ base: 'none', md: 'flex' }}>
              <Text className={styles.headerText}>{columnTitles.keepWithin}</Text>
              <RMIconButton icon={HiQuestionMarkCircle} onClick={() => openInfoModal('Restic Keep Within Duration', ResticKeepWithinTooltip)} />
            </GridItem>

            {sections.map((section, index) => (
              <React.Fragment key={index}>
                <GridItem className={styles.rowLabel}>
                  {section.title}
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
