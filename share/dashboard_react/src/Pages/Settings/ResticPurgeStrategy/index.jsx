import { Box, Grid, GridItem, Text } from '@chakra-ui/react'
import React, { useState } from 'react'
import styles from './styles.module.scss'
import NumberInput from '../../../components/NumberInput'
import TextForm from '../../../components/TextForm'
import { useDispatch } from 'react-redux'
import { setSetting } from '../../../redux/settingsSlice'
import CustomIcon from '../../../components/Icons/CustomIcon'
import RMIconButton from '../../../components/RMIconButton'
import { HiQuestionMarkCircle } from 'react-icons/hi'
import CommonModal from '../../../components/Modals/CommonModal'
import Markdown from 'react-markdown'
import remarkGfm from 'remark-gfm'

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

  const openCommonModal = () => {
    setIsCommonModalOpen(true)
  }

  const closeCommonModal = () => {
    setIsCommonModalOpen(false)
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
        <TextForm className={styles.marginCenter} value={config?.backupKeepWithin} confirmTitle={"Confirm update 'backup-keep-within':"} regexPattern="^(\d+y)?(\d+m)?(\d+d)?(\d+h)?$" onSave={(v) => { handleSave('backup-keep-within', v) }} />
      )
    },
    {
      title: "Keep Hourly",
      colA: (
        <NumberInput containerClassName={styles.marginCenter} value={config?.backupKeepHourly} confirmTitle={"Confirm update 'backup-keep-hourly':"} min={0} showEditButton={true} showConfirmModal={true} onConfirm={(v) => { handleSave('backup-keep-hourly', v) }} />
      ),
      colB: (
        <TextForm className={styles.marginCenter} value={config?.backupKeepWithinHourly} confirmTitle={"Confirm update 'backup-keep-within-hourly':"} regexPattern="^(\d+y)?(\d+m)?(\d+d)?(\d+h)?$" onSave={(v) => { handleSave('backup-keep-within-hourly', v) }} />
      )
    },
    {
      title: "Keep Daily",
      colA: (
        <NumberInput containerClassName={styles.marginCenter} value={config?.backupKeepDaily} confirmTitle={"Confirm update 'backup-keep-daily':"} min={0} showEditButton={true} showConfirmModal={true} onConfirm={(v) => { handleSave('backup-keep-daily', v) }} />
      ),
      colB: (
        <TextForm className={styles.marginCenter} value={config?.backupKeepWithinDaily} confirmTitle={"Confirm update 'backup-keep-within-daily':"} regexPattern="^(\d+y)?(\d+m)?(\d+d)?(\d+h)?$" onSave={(v) => { handleSave('backup-keep-within-daily', v) }} />
      )
    },
    {
      title: "Keep Weekly",
      colA: (
        <NumberInput containerClassName={styles.marginCenter} value={config?.backupKeepWeekly} confirmTitle={"Confirm update 'backup-keep-weekly':"} min={0} showEditButton={true} showConfirmModal={true} onConfirm={(v) => { handleSave('backup-keep-weekly', v) }} />
      ),
      colB: (
        <TextForm className={styles.marginCenter} value={config?.backupKeepWithinWeekly} confirmTitle={"Confirm update 'backup-keep-within-weekly':"} regexPattern="^(\d+y)?(\d+m)?(\d+d)?(\d+h)?$" onSave={(v) => { handleSave('backup-keep-within-weekly', v) }} />
      )
    },
    {
      title: "Keep Monthly",
      colA: (
        <NumberInput containerClassName={styles.marginCenter} value={config?.backupKeepMonthly} confirmTitle={"Confirm update 'backup-keep-monthly':"} min={0} showEditButton={true} showConfirmModal={true} onConfirm={(v) => { handleSave('backup-keep-monthly', v) }} />
      ),
      colB: (
        <TextForm className={styles.marginCenter} value={config?.backupKeepWithinMonthly} confirmTitle={"Confirm update 'backup-keep-within-monthly':"} regexPattern="^(\d+y)?(\d+m)?(\d+d)?(\d+h)?$" onSave={(v) => { handleSave('backup-keep-within-monthly', v) }} />
      )
    },
    {
      title: "Keep Yearly",
      colA: (
        <NumberInput containerClassName={styles.marginCenter} value={config?.backupKeepYearly} confirmTitle={"Confirm update 'backup-keep-yearly':"} min={0} showEditButton={true} showConfirmModal={true} onConfirm={(v) => { handleSave('backup-keep-yearly', v) }} />
      ),
      colB: (
        <TextForm className={styles.marginCenter} value={config?.backupKeepWithinYearly} confirmTitle={"Confirm update 'backup-keep-within-yearly':"} regexPattern="^(\d+y)?(\d+m)?(\d+d)?(\d+h)?$" onSave={(v) => { handleSave('backup-keep-within-yearly', v) }} />
      )
    }
  ];

  return (
    <Grid className={`${styles.container}`} templateColumns="repeat(2, 1fr)" gap={2} w="full" p={4}>
      {/* Headers */}
      <GridItem className={`${styles.label}`} p={2} textAlign="center" fontWeight="bold">
        <Text className={styles.marginCenter} textAlign={"center"}>Keep Last N</Text>
        <RMIconButton icon={HiQuestionMarkCircle} onClick={() => { setAction({ title: 'Restic Keep Last N', body: <Box><Markdown remarkPlugins={[remarkGfm]}>{ResticKeepLastNTooltip}</Markdown></Box> }); openCommonModal() }} />
      </GridItem>
      <GridItem className={`${styles.label}`} p={2} textAlign="center" fontWeight="bold">
        <Text className={styles.marginCenter} textAlign={"center"}>Keep Within Duration</Text>
        <RMIconButton icon={HiQuestionMarkCircle} onClick={() => { setAction({ title: 'Restic Keep Within Duration', body: <Box><Markdown remarkPlugins={[remarkGfm]}>{ResticKeepWithinTooltip}</Markdown></Box> }); openCommonModal() }} />
      </GridItem>

      {/* Dynamic Sections */}
      {sections.map((section, index) => (
        <React.Fragment key={index}>
          <GridItem className={`${styles.subLabel}`} colSpan={2} bg="gray.100" p={2} textAlign="center" fontWeight="bold">
            {section.title}
          </GridItem>
          <GridItem className={`${styles.value}`} p={2} textAlign="center">
            {section.colA}
          </GridItem>
          <GridItem className={`${styles.value}`} p={2} textAlign="center">
            {section.colB}
          </GridItem>
        </React.Fragment>
      ))}
      {isCommonModalOpen && (
        <CommonModal
          isOpen={isCommonModalOpen}
          size='lg'
          title={title}
          body={body}
          closeModal={() => {
            closeCommonModal()
          }}
        />
      )}
    </Grid>
  );
}

export default ResticPurgeStrategy
