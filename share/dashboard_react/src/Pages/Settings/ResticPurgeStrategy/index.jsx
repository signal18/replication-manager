import { Box, Grid, GridItem, Text, VStack } from '@chakra-ui/react'
import React, { useState } from 'react'
import styles from './styles.module.scss'
import NumberInput from '../../../components/NumberInput'
import TextForm from '../../../components/TextForm'
import { useDispatch } from 'react-redux'
import { setSetting } from '../../../redux/settingsSlice'
import RMIconButton from '../../../components/RMIconButton'
import { HiQuestionMarkCircle, HiTrash } from 'react-icons/hi'
import CommonModal from '../../../components/Modals/CommonModal'
import Markdown from 'react-markdown'
import remarkGfm from 'remark-gfm'
import { purgeResticByPolicy } from '../../../redux/clusterSlice'

function ResticPurgeStrategy({ clusterName, config }) {
  const dispatch = useDispatch()

  const [isCommonModalOpen, setIsCommonModalOpen] = useState(false)
  const [action, setAction] = useState({
    title: '',
    body: <></>
  })
  const { title, body } = action

  const ResticKeepLastNTooltip = `
The number of snapshots to keep.  
If set to 1, only the last snapshot will be kept.  
If set to 2, the last 2 snapshots will be kept, and so on.  
If set to 0, the argument will be omitted and snapshots might be purged unless other rules apply.  
Restic will keep the last N snapshots for each category like using OR condition.`

  const ResticKeepWithinTooltip = `
The duration format is a sequence of decimal numbers, each with a unit suffix.  
Valid time units are "h", "m", "d", "y".  
For example, "2h" means 2 hours, "1d" means 1 day. It also supports multiple units like "1d2h".  
Empty value will be omitted and snapshots might be purged unless other rules apply.  
Restic will keep snapshots within the duration for each category like using OR condition.  
The duration will be calculated from the time of the last snapshot.  
`

  const columnTitles = {
    keepLast: 'Keep Last N',
    keepWithin: 'Keep Within Duration'
  }

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
        <Box className={styles.infoTooltip}>
          <Markdown remarkPlugins={[remarkGfm]}>{tooltip}</Markdown>
        </Box>
      )
    })
    openCommonModal()
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
      <Box className={styles.infoBox} p={2} borderWidth="1px" borderRadius="md" bg="gray.50">
        <RMIconButton icon={HiTrash} confirm={true} onClick={() => dispatch(purgeResticByPolicy({clusterName}))} />
        <Text as="span">Click the trash icon to purge restic backups according to the defined retention policy.</Text>
      </Box>
            
      {isCommonModalOpen && (
        <CommonModal
          isOpen={isCommonModalOpen}
          size='lg'
          title={title}
          body={body}
          contentClassName={styles.infoModalContent}
          headerClassName={styles.infoModalHeader}
          bodyClassName={styles.infoModalBody}
          closeModal={() => {
            closeCommonModal()
          }}
        />
      )}
    </VStack>
  );
}

export default ResticPurgeStrategy
