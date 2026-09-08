import { Box, HStack, Input } from '@chakra-ui/react'
import React, { useState, useEffect } from 'react'
import styles from './styles.module.scss'
import modalStyles from '../../components/Modals/styles.module.scss'
import RMSwitch from '../../components/RMSwitch'
import Dropdown from '../../components/Dropdown'
import RMButton from '../../components/RMButton'
import RMIconButton from '../../components/RMIconButton'
import TableType2 from '../../components/TableType2'
import ConfirmModal from '../../components/Modals/ConfirmModal'
import CommonModal from '../../components/Modals/CommonModal'
import Markdown from 'react-markdown'
import remarkGfm from 'remark-gfm'
import { HiQuestionMarkCircle } from 'react-icons/hi'
import { useDispatch } from 'react-redux'
import { setSetting, switchSetting } from '../../redux/settingsSlice'

// A single value setting: text/number/duration input with a confirmed Save.
function EditableSetting({ clusterName, setting, initial, placeholder, isDisabled }) {
  const dispatch = useDispatch()
  const [val, setVal] = useState(initial ?? '')
  const [confirmOpen, setConfirmOpen] = useState(false)
  useEffect(() => {
    setVal(initial ?? '')
  }, [initial])
  const dirty = String(val) !== String(initial ?? '')
  return (
    <HStack>
      <Input size='sm' width='130px' placeholder={placeholder} value={val} isDisabled={isDisabled} onChange={(e) => setVal(e.target.value)} />
      <RMButton size='sm' isDisabled={isDisabled || !dirty} onClick={() => setConfirmOpen(true)}>Save</RMButton>
      {confirmOpen && (
        <ConfirmModal
          isOpen={confirmOpen}
          closeModal={() => setConfirmOpen(false)}
          title={`Confirm ${setting} = ${val}`}
          onConfirmClick={() => {
            dispatch(setSetting({ clusterName, setting, value: String(val) }))
            setConfirmOpen(false)
          }}
        />
      )}
    </HStack>
  )
}

// Dynamic resource management: the switch, the alignment mode, the safety margins,
// and the client-settable per-action scale SPEEDS. Grouped here under one retractable tab.
function DynamicConfigSettings({ selectedCluster, user }) {
  const dispatch = useDispatch()
  const cfg = selectedCluster?.config || {}
  const clusterName = selectedCluster?.name
  const disabled = user?.grants?.['cluster-settings'] === false

  const [isInfoOpen, setIsInfoOpen] = useState(false)
  const [info, setInfo] = useState({ title: '', body: <></> })
  const openInfo = (title, content) => {
    setInfo({ title, body: <Box className={modalStyles.infoTooltip}><Markdown remarkPlugins={[remarkGfm]}>{content}</Markdown></Box> })
    setIsInfoOpen(true)
  }
  const h = (content, title) => (
    <RMIconButton icon={HiQuestionMarkCircle} onClick={() => openInfo(title, content)} iconFontsize='1rem' variant='ghost' style={{ opacity: 0.5, minWidth: '1.5rem', height: '1.5rem' }} />
  )

  const num = (setting, initial, ph) => <EditableSetting clusterName={clusterName} setting={setting} initial={initial} placeholder={ph} isDisabled={disabled} />

  const dataObject = [
    {
      key: 'Apply Dynamic Config On Change Tags',
      help: h(`**Apply Dynamic Config**\n\nWhen enabled, config changes are applied dynamically via \`SET GLOBAL\` (no restart); restart-only variables are written for the next restart.\n\nConfig: \`prov-db-apply-dynamic-config\``, 'Apply Dynamic Config'),
      value: (<RMSwitch confirmTitle={'Confirm switch settings for prov-db-apply-dynamic-config?'} onChange={() => dispatch(switchSetting({ clusterName, setting: 'prov-db-apply-dynamic-config' }))} isDisabled={disabled} isChecked={cfg.provDBApplyDynamicConfig} />)
    },
    {
      key: 'Apply Dynamic Resource Resize',
      help: h(`**Dynamic Resource Resize**\n\nWhen on, a provisioned-memory change (e.g. a DBU/plan resize) is applied to the RUNNING database live (SET GLOBAL + cgroup via orchestrator/client hook) instead of a restart. Restart-only vars still schedule a restart. Optional hooks: \`prov-db-dynamic-resource-can-change-script\` (feasibility) and \`prov-db-dynamic-resource-change-script\` (does the infra resize).\n\nConfig: \`prov-db-dynamic-resource\``, 'Apply Dynamic Resource Resize'),
      value: (<RMSwitch confirmTitle={'Confirm switch settings for prov-db-dynamic-resource?'} onChange={() => dispatch(switchSetting({ clusterName, setting: 'prov-db-dynamic-resource' }))} isDisabled={disabled} isChecked={cfg.provDbDynamicResource} />)
    },
    {
      key: 'Auto-Update Compliance',
      help: h(`**Auto-Update Compliance**\n\nWhen enabled, repman regenerates the config from a new compliance module automatically (repman side).\n\nConfig: \`prov-auto-update-compliance\``, 'Auto-Update Compliance'),
      value: (<RMSwitch confirmTitle={'Confirm switch settings for prov-auto-update-compliance?'} onChange={() => dispatch(switchSetting({ clusterName, setting: 'prov-auto-update-compliance' }))} isDisabled={disabled} isChecked={cfg.provAutoUpdateCompliance} />)
    },
    {
      key: 'Auto-Agree Compliance',
      help: h(`**Auto-Agree Compliance**\n\nWhen enabled, a config VALUE delta is auto-agreed to the compliance value and pushed to the DB (value changes only; dropped/deprecated/unknown vars always stay for manual review). Disabled = manual review. DB side.\n\nConfig: \`prov-db-compliance-auto-agree\``, 'Auto-Agree Compliance'),
      value: (<RMSwitch confirmTitle={'Confirm switch settings for prov-db-compliance-auto-agree?'} onChange={() => dispatch(switchSetting({ clusterName, setting: 'prov-db-compliance-auto-agree' }))} isDisabled={disabled} isChecked={cfg.provDbComplianceAutoAgree} />)
    },
    {
      key: 'Resource Alignment',
      help: h(`**Resource Alignment**\n\nAligns the container memory cap to the DBU tier.\n\n- **plan** (default): tier = the plan / node count\n- **up**: tier = max-axis config DBU (coherence/debug)\n- **off**: cap = prov-db-memory (legacy)\n\nConfig: \`prov-db-resource-align\``, 'Resource Alignment'),
      value: (
        <Dropdown
          options={[{ value: 'plan', label: 'plan (default)' }, { value: 'up', label: 'up' }, { value: 'off', label: 'off' }]}
          selectedValue={cfg.provDbResourceAlign || 'plan'}
          confirmTitle='Confirm resource alignment: '
          onChange={(value) => dispatch(setSetting({ clusterName, setting: 'prov-db-resource-align', value }))}
        />
      )
    },
    {
      key: 'Saturation Margin %',
      help: h(`**Saturation safety margin (high-water)**\n\nA server is "over" a reference (config or plan) on an axis when it consumes >= (1 - pct/100) of it. Drives raise-resources & cap-up.\n\nConfig: \`prov-db-cap-safety-pct\` (default 15 -> fires at 85%)`, 'Saturation Margin'),
      value: num('prov-db-cap-safety-pct', cfg.provDbCapSafetyPct, '15')
    },
    {
      key: 'Shrink Margin %',
      help: h(`**Shrink margin (low-water)**\n\nA server is "under" a reference when it consumes <= (pct/100) of it. Drives shrink-resources & cap-down. The dead-band between shrink-pct and (100 - safety-pct) is status quo.\n\nConfig: \`prov-db-cap-shrink-pct\` (default 50)`, 'Shrink Margin'),
      value: num('prov-db-cap-shrink-pct', cfg.provDbCapShrinkPct, '50')
    },
    {
      key: 'Overcommit % (scale-up ceiling)',
      help: h(`**Commercial scalability-up barrier**\n\nMax percent the dynamic resource change may auto-grow the plan (plan x (1 + pct/100)) before a manual plan raise is required.\n\nConfig: \`prov-db-overcommit-pct\` (default 50)`, 'Overcommit %'),
      value: num('prov-db-overcommit-pct', cfg.provDbOvercommitPct, '50')
    },
    {
      key: 'Cap Burst DBU',
      help: h(`**Container cap burst (technical headroom)**\n\nBurst DBU added on top of the DBU tier for the container memory cap, so mariadbd has room above prov-db-memory and is not OOM-killed. NOT overcommit; the plan is never modified.\n\nConfig: \`prov-db-cap-burst-dbu\` (default 1)`, 'Cap Burst DBU'),
      value: num('prov-db-cap-burst-dbu', cfg.provDbCapBurstDbu, '1')
    },
    {
      key: 'Scale-Up Speed (resources, in-plan)',
      help: h(`**Scale-up speed — resources within the plan**\n\nHow long a server's config saturation must persist before repman scales its resources UP (free within the plan). A duration.\n\nConfig: \`prov-db-scale-up-config-in-plan-speed\` (default 1m)`, 'Scale-Up Speed (resources)'),
      value: num('prov-db-scale-up-config-in-plan-speed', cfg.scaleUpConfigInPlanSpeed, '1m')
    },
    {
      key: 'Scale-Down Speed (resources, in-plan)',
      help: h(`**Scale-down speed — resources within the plan**\n\nHow long under-use must persist before repman scales resources DOWN (slower than up, avoids thrashing). A duration.\n\nConfig: \`prov-db-scale-down-config-in-plan-speed\` (default 5m)`, 'Scale-Down Speed (resources)'),
      value: num('prov-db-scale-down-config-in-plan-speed', cfg.scaleDownConfigInPlanSpeed, '5m')
    },
    {
      key: 'Scale-Up Speed (plan / cap up)',
      help: h(`**Scale-up speed — the plan (cap up)**\n\nHow long consumption must persist against the plan before repman raises the plan. Commercial, so slower than in-plan. A duration.\n\nConfig: \`prov-db-scale-up-plan-speed\` (default 30m)`, 'Scale-Up Speed (plan)'),
      value: num('prov-db-scale-up-plan-speed', cfg.scaleUpPlanSpeed, '30m')
    },
    {
      key: 'Scale-Down Speed (plan / cap down)',
      help: h(`**Scale-down speed — the plan (cap down)**\n\nHow long under-use must persist before repman lowers the plan. Most conservative (don't yo-yo the billed plan). A duration.\n\nConfig: \`prov-db-scale-down-plan-speed\` (default 1h)`, 'Scale-Down Speed (plan)'),
      value: num('prov-db-scale-down-plan-speed', cfg.scaleDownPlanSpeed, '1h')
    }
  ]

  return (
    <>
      <TableType2 dataArray={dataObject} className={styles.tableWithHelp} helpColumn={true} />
      <CommonModal isOpen={isInfoOpen} closeModal={() => setIsInfoOpen(false)} title={info.title} body={info.body} size='xl' />
    </>
  )
}

export default DynamicConfigSettings
