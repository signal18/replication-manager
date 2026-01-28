import { Box, Grid, GridItem, HStack, Text, VStack } from '@chakra-ui/react'
import React, { useState } from 'react'
import styles from './styles.module.scss'
import NumberInput from '../../../components/NumberInput'
import TextForm from '../../../components/TextForm'
import { useDispatch } from 'react-redux'
import { setSetting } from '../../../redux/settingsSlice'
import RMIconButton from '../../../components/RMIconButton'
import { HiQuestionMarkCircle, HiTrash } from 'react-icons/hi'
import CommonModal from '../../../components/Modals/CommonModal'
import modalStyles from '../../../components/Modals/styles.module.scss'
import Markdown from 'react-markdown'
import remarkGfm from 'remark-gfm'
import { purgeResticByPolicy } from '../../../redux/clusterSlice'

function ResticPurgeStrategy({ clusterName, config }) {
  const dispatch = useDispatch()

  const joinClasses = (...classes) => classes.filter(Boolean).join(' ')

  const [isCommonModalOpen, setIsCommonModalOpen] = useState(false)
  const [action, setAction] = useState({
    title: '',
    body: <></>
  })
  const { title, body } = action

  const ResticKeepLastNTooltip = `
Choose how many recent snapshots to keep for this section.  
Example: 3 keeps the latest 3 snapshots.  
Set to 0 to disable this rule.  
We apply both keep-last and keep-within settings. A snapshot stays if it matches either one.`

  const ResticKeepWithinTooltip = `
Keep snapshots from the most recent time window in this section.  
Examples: "2h", "1d", "1d2h".  
Units: h (hours), d (days), m (months), y (years).  
Leave blank to disable this rule.  
We apply both keep-last and keep-within settings. A snapshot stays if it matches either one.  
The window is counted back from when the purge runs.  
`

  const columnTitles = {
    keepLast: 'Keep Last N',
    keepWithin: 'Keep Within Duration'
  }

  const ResticPurgeGroupByTooltip = `
Override restic group-by.  
Examples: "host", "paths", "host,paths", "tags".  
Allowed values: host, paths, tags.  
Use "default" for restic defaults or "none" for a single group.  
host: separate retention per client hostname.  
paths: separate retention per source path.  
tags: separate retention per tag set.  
Leave blank to use restic defaults.`

  const ResticKeepTagTooltip = `
Restic keep-tag protects snapshots from purge when they include specific tags.  
Provide space-separated tags, e.g. "line:adhoc env:prod".  
Commas inside a tag mean AND in restic (use quotes if needed).  
You can use {cluster} or {tenant} placeholders (for example: "cluster:{cluster}").  
Wrap a literal tag in quotes to prevent placeholder processing.  
Leave empty to disable.
`

  const openCommonModal = () => {
    setIsCommonModalOpen(true)
  }

  const closeCommonModal = () => {
    setIsCommonModalOpen(false)
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

  const buildForgetCommand = () => {
    const args = ['restic', 'forget', '--prune']
    const groupBy = config?.backupResticPurgeGroupBy?.trim()
    if (groupBy && groupBy.toLowerCase() !== 'default') {
      if (groupBy.toLowerCase() === 'none') {
        args.push('--group-by', "''")
      } else {
        args.push('--group-by', groupBy)
      }
    }

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

    return args.join(' ')
  }

  const handleSave = (key, value) => {
    dispatch(setSetting({
      clusterName: clusterName,
      setting: key,
      value
    }))
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
    <VStack spacing={2} align="stretch" w={"100%"}>
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
            className={styles.marginCenter}
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
            className={styles.marginCenter}
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
      <Grid
        className={`${styles.container}`}
        templateColumns={{ base: '1fr', md: 'minmax(140px, 0.7fr) minmax(220px, 1fr) minmax(240px, 1fr)' }}
        columnGap={3}
        rowGap={2}
        w="full"
      >
        {/* Headers (desktop) */}
        <GridItem className={styles.headerCell} display={{ base: 'none', md: 'flex' }} />
        <GridItem className={styles.headerCell} display={{ base: 'none', md: 'flex' }}>
          <Text className={styles.headerText}>{columnTitles.keepLast}</Text>
          <RMIconButton icon={HiQuestionMarkCircle} onClick={() => openInfoModal('Restic Keep Last N', ResticKeepLastNTooltip)} />
        </GridItem>
        <GridItem className={styles.headerCell} display={{ base: 'none', md: 'flex' }}>
          <Text className={styles.headerText}>{columnTitles.keepWithin}</Text>
          <RMIconButton icon={HiQuestionMarkCircle} onClick={() => openInfoModal('Restic Keep Within Duration', ResticKeepWithinTooltip)} />
        </GridItem>

        {/* Dynamic Sections */}
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
      <Box className={styles.commandBox} borderWidth="1px" borderRadius="md">
        <Text className={styles.commandLabel}>Command preview</Text>
        <Text className={styles.commandText}>{buildForgetCommand()}</Text>
        <Text className={styles.commandHint}>Updates as you change the policy.</Text>
      </Box>
      <Box className={styles.infoBox} p={2} borderWidth="1px" borderRadius="md" bg="gray.50">
        <RMIconButton icon={HiTrash} confirm={true} onClick={() => dispatch(purgeResticByPolicy({clusterName}))} />
        <Text as="span">Use the trash icon to run a purge with the current retention policy.</Text>
      </Box>
            
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
    </VStack>
  );
}

export default ResticPurgeStrategy
