import { Box, Flex, Slider, SliderFilledTrack, SliderThumb, SliderTrack, Text, Tooltip } from '@chakra-ui/react'
import TextForm from '../../../../components/TextForm';
import styles from './styles.module.scss';
import { useDispatch } from 'react-redux';
import TableType2 from '../../../../components/TableType2';
import { previewAppTemplateContent, previewResetAppTemplateImpact, resetAppFromTemplate, saveAppAsTemplate, setAppSetting } from '../../../../redux/settingsSlice';
import Checkboxes from '../../../../components/Checkboxes/Checkboxes';
import React, { useCallback, useMemo, useState } from 'react';
import Dropdown from '../../../../components/Dropdown';
import RMIconButton from '../../../../components/RMIconButton';
import { AiOutlineSave } from 'react-icons/ai';
import { HiEye, HiRefresh } from 'react-icons/hi';
import VariableInputArea from '../../../../components/VariableTree/VariableInputArea';
import PropTypes from 'prop-types';
import CopyTextModal from '../../../../components/Modals/CopyTextModal';
import ConfirmModal from '../../../../components/Modals/ConfirmModal';
import { convertSize } from '../../../../utility/common';
import Gauge from '../../../../components/Gauge';

function AppUnitSlider({ value, min, max, step, isDisabled, formatFn, onChange }) {
  const [draft, setDraft] = useState(null)
  const [showTooltip, setShowTooltip] = useState(false)
  const current = draft !== null ? draft : value
  const safeMin = min > 0 ? min : 1
  const safeMax = max > safeMin ? max : safeMin + 256

  return (
    <Box w='100%'>
      <Flex justify='space-between' mb={1}>
        <Text fontSize='sm' fontWeight='bold' color='var(--text-color)'>App Unit</Text>
        <Text fontSize='sm' fontWeight='semibold' color='var(--text-color)'>{formatFn(current)}</Text>
      </Flex>
      <Slider
        min={safeMin}
        max={safeMax}
        step={step > 0 ? step : 1}
        value={Math.max(current, safeMin)}
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
          <SliderFilledTrack bg='teal.400' />
        </SliderTrack>
        <Tooltip label={formatFn(current)} placement='top' isOpen={showTooltip || draft !== null} hasArrow>
          <SliderThumb boxSize={5} bg='teal.500' />
        </Tooltip>
      </Slider>
      <Flex justify='space-between' mt={1}>
        <Text fontSize='9px' color='gray.500'>{safeMin} App Unit</Text>
        <Text fontSize='9px' color='gray.500'>{safeMax} App Unit</Text>
      </Flex>
    </Box>
  )
}

AppUnitSlider.propTypes = {
  value: PropTypes.number,
  min: PropTypes.number,
  max: PropTypes.number,
  step: PropTypes.number,
  isDisabled: PropTypes.bool,
  formatFn: PropTypes.func,
  onChange: PropTypes.func,
}

const GeneralSection = ({ clusterName, appId, appName, appHost, config, appConfig, dockerTemplates, substitution, user }) => {

  const dispatch = useDispatch();
  const [isTemplatePreviewOpen, setIsTemplatePreviewOpen] = useState(false)
  const [templatePreviewContent, setTemplatePreviewContent] = useState('')
  const [templatePreviewTitle, setTemplatePreviewTitle] = useState('Template Preview')
  const [isResetImpactModalOpen, setIsResetImpactModalOpen] = useState(false)
  const [resetImpactTemplate, setResetImpactTemplate] = useState('')
  const [resetImpactForceRefresh, setResetImpactForceRefresh] = useState(false)
  const [resetImpactChanges, setResetImpactChanges] = useState([])
  const haTopologyOptions = useMemo(() => ([{ value: 'failover', name: 'Failover' }, { value: 'flex', name: 'Flex' }]), []);
  const templateOptions = useMemo(() => {
    const templateList = Array.isArray(dockerTemplates) ? dockerTemplates : []
    return [{ name: 'Select Template', value: '' }, ...templateList.map(item => ({ name: item, value: item }))]
  }, [dockerTemplates])
  const {
    provAppDockerImg = '', provAppDockerCmd = '', provAppTemplate = '',
    provAppAgents = '', provAppHaTopology = '', provAppCreditPlanned = 0,
    provAppSizingMode: appSizingMode = '', provAppCpuCores = '', provAppMemory = '', provAppDiskSize = ''
  } = appConfig;

  // Effective mode resolution mirrors backend logic:
  // 1) app override when cluster allows it
  // 2) cluster mode when set
  // 3) existing app-stamped mode when cluster mode is unset
  // 4) legacy when neither is set
  const clusterSizingMode = config?.provAppSizingMode || ''
  const provAppSizingMode = appSizingMode || clusterSizingMode
  const isUnitMode = provAppSizingMode === 'unit'
  const isManualMode = provAppSizingMode === 'manual'
  // Effective mode is empty: neither cluster nor app has set a sizing policy.
  const isLegacyMode = !isUnitMode && !isManualMode
  // Cluster policy says "unit" but this app itself is not explicitly unit-managed yet.
  // Preserve raw stored values on read; the first unit-based write normalizes them.
  const isLegacyAppInUnitCluster = isUnitMode && appSizingMode !== 'unit'

  const baseCore = 1
  const baseMem = 4096
  const baseDisk = 10

  // Derive unit from raw stored resources (used for legacy apps that haven't been normalised yet).
  const derivedUnitFromResources = useMemo(() => {
    const cores = parseInt(provAppCpuCores) || 0
    const memMB = parseFloat(convertSize(provAppMemory, 'M', 'M')) || 0
    const diskGB = parseFloat(convertSize(provAppDiskSize, 'G', 'G')) || 0
    if (!cores && !memMB && !diskGB) return 1
    return Math.max(1, Math.ceil(Math.max(
      cores ? cores / baseCore : 0,
      memMB ? memMB / baseMem : 0,
      diskGB ? diskGB / baseDisk : 0
    )))
  }, [provAppCpuCores, provAppMemory, provAppDiskSize])

  const creditStep = useMemo(() => {
    const raw = provAppAgents
    const list = typeof raw === 'string' ? raw.split(',').filter((a) => a.trim()) : (Array.isArray(raw) ? raw.filter(Boolean) : [])
    return list.length || 1
  }, [provAppAgents])

  const clusterCredits = config?.cloud18ApplicationCredits || 0
  const clusterCreditsUsed = config?.cloud18ApplicationCreditsUsed || 0
  const clusterCreditsAvailable = clusterCredits > 0 ? clusterCredits - clusterCreditsUsed + provAppCreditPlanned : 0

  const appUnitIsValid = isLegacyAppInUnitCluster
    ? true
    : isUnitMode && creditStep > 0 && provAppCreditPlanned > 0
    ? provAppCreditPlanned % creditStep === 0
    : true
  const appUnitValue = (appUnitIsValid && creditStep > 0 && provAppCreditPlanned > 0)
    ? (isLegacyAppInUnitCluster ? derivedUnitFromResources : provAppCreditPlanned / creditStep)
    : (isLegacyAppInUnitCluster ? derivedUnitFromResources : 0)
  const maxAppUnit = 256

  const sliderDisplayValue = useMemo(() => {
    if (isUnitMode && !isLegacyAppInUnitCluster && appUnitIsValid && creditStep > 0 && provAppCreditPlanned > 0) {
      return provAppCreditPlanned / creditStep
    }
    return derivedUnitFromResources || 1
  }, [isUnitMode, isLegacyAppInUnitCluster, appUnitIsValid, creditStep, provAppCreditPlanned, derivedUnitFromResources])

  const formatAppUnit = useCallback((unit) => {
    const mem = unit * baseMem
    const memLabel = mem >= 1024 ? `${mem / 1024}GB` : `${mem}MB`
    return `${unit} App Unit — ${unit * baseCore} cores, ${memLabel} mem, ${unit * baseDisk}GB disk per agent`
  }, [])

  const agentList = useMemo(() => {
    const raw = config?.provAppAgents || config?.provDbAgents
    if (Array.isArray(raw)) return raw
    if (typeof raw === 'string' && raw.trim()) return raw.split(',').map((s) => s.trim()).filter(Boolean)
    return []
  }, [config?.provAppAgents, config?.provDbAgents])

  const onSaveDockerImage = useCallback((value) => dispatch(setAppSetting({ clusterName: clusterName, appId: appId, setting: 'prov-app-docker-img', value: value })), [clusterName, appId, dispatch])
  const onSaveDockerCmd = useCallback((value) => dispatch(setAppSetting({ clusterName: clusterName, appId: appId, setting: 'prov-app-docker-cmd', value: value })), [clusterName, appId, dispatch])
  const onSaveAppAsTemplate = useCallback(() => dispatch(saveAppAsTemplate({ clusterName: clusterName, appId: appId, template: appName })), [clusterName, appId, appName, dispatch])
  const onResetAppFromTemplate = useCallback((value) => {
    const templateName = typeof value === 'string' ? value : value?.value || value?.name || ''
    if (!templateName) return
    dispatch(previewResetAppTemplateImpact({ clusterName, appId, template: templateName, forceRefresh: false }))
      .unwrap()
      .then(({ data }) => {
        setResetImpactTemplate(templateName)
        setResetImpactForceRefresh(false)
        setResetImpactChanges(Array.isArray(data?.changes) ? data.changes : [])
        setIsResetImpactModalOpen(true)
      })
  }, [clusterName, appId, dispatch])
  const onRefreshAndResetAppFromTemplate = useCallback(() => {
    if (!provAppTemplate) {
      return
    }
    dispatch(previewResetAppTemplateImpact({ clusterName, appId, template: provAppTemplate, forceRefresh: true }))
      .unwrap()
      .then(({ data }) => {
        setResetImpactTemplate(provAppTemplate)
        setResetImpactForceRefresh(true)
        setResetImpactChanges(Array.isArray(data?.changes) ? data.changes : [])
        setIsResetImpactModalOpen(true)
      })
  }, [clusterName, appId, provAppTemplate, dispatch])
  const onConfirmResetFromTemplate = useCallback(() => {
    if (!resetImpactTemplate) return
    dispatch(resetAppFromTemplate({ clusterName, appId, template: resetImpactTemplate, forceRefresh: resetImpactForceRefresh }))
    setIsResetImpactModalOpen(false)
  }, [clusterName, appId, resetImpactTemplate, resetImpactForceRefresh, dispatch])

  const onAgentsChange = useCallback((value) => {
    const newList = Array.isArray(value)
      ? value.filter(Boolean)
      : (typeof value === 'string' ? value.split(',').filter(a => a.trim()) : [])
    // Always dispatch agent update; backend handles credit recalculation for unit mode
    dispatch(setAppSetting({ clusterName, appId, setting: 'prov-app-agents', value: newList.join(',') }))
  }, [clusterName, appId, dispatch])

  const onHATopologyChange = useCallback((value) => { dispatch(setAppSetting({ clusterName: clusterName, appId: appId, setting: 'prov-app-ha-topology', value: value })) }, [clusterName, appId, dispatch])
  const onPreviewTemplate = useCallback(() => {
    if (!provAppTemplate) {
      return
    }
    dispatch(previewAppTemplateContent({ clusterName, templateName: provAppTemplate }))
      .unwrap()
      .then(({ data }) => {
        setTemplatePreviewTitle(`Template Preview: ${data?.name || provAppTemplate}`)
        setTemplatePreviewContent(data?.content || '')
        setIsTemplatePreviewOpen(true)
      })
  }, [clusterName, provAppTemplate, dispatch])

  const [appUnitConfirmState, setAppUnitConfirmState] = useState({ isOpen: false, title: '', handler: null })
  const closeAppUnitConfirm = useCallback(() => setAppUnitConfirmState({ isOpen: false, title: '', handler: null }), [])

  const dataObject = useMemo(() => {
    const rows = [
      {
        key: 'App Name',
        value: (<Text>{appName}</Text>)
      },
      {
        key: 'App Host',
        value: (<Text>{appHost}</Text>)
      },
      {
        key: 'Docker Image',
        value: (
          <TextForm
            value={provAppDockerImg}
            confirmTitle="Docker Image Change"
            confirmBody='Are you sure you want to change "prov-app-docker-img" to: '
            onSave={onSaveDockerImage}
          />
        )
      },
      {
        key: 'Docker Command',
        value: (
          <VariableInputArea
            name={`docker-cmd`}
            value={provAppDockerCmd}
            useConfirmModal={true}
            confirmTitle={"Docker command changed"}
            variables={substitution}
            placeholder="Docker cmd"
            onSave={onSaveDockerCmd}
            className={styles.variableInputArea}
          />
        )
      },
      {
        key: 'Docker Template',
        value: (
          <Flex alignItems='center' gap='8px'>
            <Text>{provAppTemplate}</Text>
            <RMIconButton
              icon={HiEye}
              aria-label='Preview template content'
              tooltip='Preview template content'
              onClick={onPreviewTemplate}
              isDisabled={!provAppTemplate}
            />
          </Flex>
        )
      },
      {
        key: 'Reset App From Template',
        value: (
          <Flex alignItems='center' gap='8px'>
            <Dropdown
              isMenuPortalTarget={true}
              onChange={onResetAppFromTemplate}
              selectedValue={provAppTemplate}
              options={templateOptions}
            />
            <RMIconButton
              icon={HiRefresh}
              aria-label='Refresh template from source and apply'
              tooltip='Refresh current template from source and apply'
              onClick={onRefreshAndResetAppFromTemplate}
              isDisabled={!provAppTemplate}
              confirm={true}
              confirmTitle='Refresh and Apply Template'
              confirmBody='Are you sure you want to refresh the current template from source and apply it to this app?'
            />
          </Flex>
        )
      },
      {
        key: 'Save As Template',
        value: (
          <RMIconButton
            icon={AiOutlineSave}
            aria-label="Save App As Template"
            tooltip="Save App As Template"
            onClick={onSaveAppAsTemplate}
            isDisabled={user?.grants['app-deployment'] == false}
            confirmTitle="Save App As Template"
          />
        )
      },
      {
        key: 'OpenSVC Agents',
        value: (
          <Checkboxes
            list={agentList}
            values={provAppAgents}
            confirm={true}
            splitConfirm={true}
            confirmTitle={isUnitMode && !isLegacyAppInUnitCluster
              ? `Confirm agent change — App Unit stays at ${appUnitValue || 1} per agent, planned credits will be updated`
              : `Confirm agent change`}
            onChange={onAgentsChange}
            parentStyles={styles}
          />
        )
      },
      {
        key: 'OpenSVC HA Topology',
        value: (
          <Dropdown
            confirmTitle="OpenSVC HA Topology Change"
            confirmBody='Are you sure you want to change "prov-app-ha-topology" to: '
            isMenuPortalTarget={true}
            onChange={onHATopologyChange}
            options={haTopologyOptions}
            selectedValue={provAppHaTopology}
          />
        )
      },
    ]

    rows.push({
      key: 'Resources',
      value: (
        <Flex direction='column' gap={4} w='100%'>
          {isUnitMode && isLegacyAppInUnitCluster && (
            <Text fontSize='xs' color='orange.500'>
              Legacy resource values preserved. The next App Unit edit will normalize this app (derived: {derivedUnitFromResources} App Unit).
            </Text>
          )}
          {isUnitMode && !appUnitIsValid && (
            <Text fontSize='sm' color='red.500'>
              Inconsistent state: {provAppCreditPlanned} credit{provAppCreditPlanned !== 1 ? 's' : ''} cannot be evenly distributed across {creditStep} agent{creditStep !== 1 ? 's' : ''}. Update agents or credits to resolve.
            </Text>
          )}
          <AppUnitSlider
            isDisabled={user?.grants['app-config'] == false}
            value={sliderDisplayValue}
            min={1}
            max={maxAppUnit}
            step={1}
            formatFn={formatAppUnit}
            onChange={(unit) => {
              const credits = unit * creditStep
              const needsModeSwitch = provAppSizingMode !== 'unit'
              const exceedsPool = clusterCredits > 0 && clusterCreditsAvailable > 0 && credits > clusterCreditsAvailable
              const poolWarning = exceedsPool ? ` — ⚠ exceeds available pool (${Math.floor(clusterCreditsAvailable / creditStep)} App Unit free)` : ''
              setAppUnitConfirmState({
                isOpen: true,
                title: needsModeSwitch
                  ? `Switch to App Unit: ${unit} App Unit — ${formatAppUnit(unit)} × ${creditStep} agent(s) = ${credits} credits${poolWarning}`
                  : `Confirm App Unit change to ${unit} — ${formatAppUnit(unit)} × ${creditStep} agent(s) = ${credits} credits${poolWarning}`,
                handler: () => {
                  const saveCredits = () => dispatch(setAppSetting({ clusterName, appId, setting: 'prov-app-credit-planned', value: credits }))
                  if (needsModeSwitch) {
                    dispatch(setAppSetting({ clusterName, appId, setting: 'prov-app-sizing-mode', value: 'unit' })).unwrap().then(saveCredits)
                  } else {
                    saveCredits()
                  }
                }
              })
            }}
          />
          <Flex className={styles.resources} flexWrap='wrap'>
            <Gauge
              isDisabled={user?.grants['app-config'] == false}
              minValue={256}
              maxValue={262144}
              value={parseFloat(convertSize(provAppMemory, 'M', 'M')) || 0}
              text={'Memory'}
              width={150}
              height={105}
              hideMinMax={false}
              showStep={true}
              step={256}
              appendTextToValue='MB'
              handleStepChange={(value) => {
                const needsModeSwitch = provAppSizingMode !== 'manual'
                const memLabel = value >= 1024 ? `${value / 1024}GB` : `${value}MB`
                setAppUnitConfirmState({
                  isOpen: true,
                  title: needsModeSwitch ? `Switch to Manual — set memory to ${memLabel}` : `Confirm memory change to ${memLabel}`,
                  handler: () => {
                    const save = () => dispatch(setAppSetting({ clusterName, appId, setting: 'prov-app-memory', value: String(value) }))
                    if (needsModeSwitch) {
                      dispatch(setAppSetting({ clusterName, appId, setting: 'prov-app-sizing-mode', value: 'manual' })).unwrap().then(save)
                    } else { save() }
                  }
                })
              }}
            />
            <Gauge
              isDisabled={user?.grants['app-config'] == false}
              minValue={1}
              maxValue={10000}
              value={parseFloat(convertSize(provAppDiskSize, 'G', 'G')) || 0}
              text={'Disk size'}
              width={150}
              height={105}
              hideMinMax={false}
              showStep={true}
              step={10}
              appendTextToValue='GB'
              handleStepChange={(value) => {
                const needsModeSwitch = provAppSizingMode !== 'manual'
                setAppUnitConfirmState({
                  isOpen: true,
                  title: needsModeSwitch ? `Switch to Manual — set disk to ${value}GB` : `Confirm disk size change to ${value}GB`,
                  handler: () => {
                    const save = () => dispatch(setAppSetting({ clusterName, appId, setting: 'prov-app-disk-size', value: String(value) }))
                    if (needsModeSwitch) {
                      dispatch(setAppSetting({ clusterName, appId, setting: 'prov-app-sizing-mode', value: 'manual' })).unwrap().then(save)
                    } else { save() }
                  }
                })
              }}
            />
            <Gauge
              isDisabled={user?.grants['app-config'] == false}
              minValue={1}
              maxValue={256}
              value={parseInt(provAppCpuCores) || 0}
              text={'Cores'}
              width={150}
              height={105}
              hideMinMax={false}
              showStep={true}
              step={1}
              handleStepChange={(value) => {
                const needsModeSwitch = provAppSizingMode !== 'manual'
                setAppUnitConfirmState({
                  isOpen: true,
                  title: needsModeSwitch ? `Switch to Manual — set cores to ${value}` : `Confirm CPU cores change to ${value}`,
                  handler: () => {
                    const save = () => dispatch(setAppSetting({ clusterName, appId, setting: 'prov-app-cpu-cores', value: String(value) }))
                    if (needsModeSwitch) {
                      dispatch(setAppSetting({ clusterName, appId, setting: 'prov-app-sizing-mode', value: 'manual' })).unwrap().then(save)
                    } else { save() }
                  }
                })
              }}
            />
          </Flex>
        </Flex>
      )
    })

    return rows
  }, [
    appName, appHost, provAppDockerImg, onSaveDockerImage, provAppDockerCmd, onSaveDockerCmd,
    onSaveAppAsTemplate, templateOptions, provAppTemplate, onPreviewTemplate,
    onResetAppFromTemplate, onRefreshAndResetAppFromTemplate,
    agentList, onAgentsChange, provAppAgents, onHATopologyChange, provAppHaTopology, haTopologyOptions,
    isUnitMode, isManualMode, isLegacyMode,
    appSizingMode, clusterSizingMode, provAppSizingMode,
    isLegacyAppInUnitCluster, derivedUnitFromResources,
    substitution, user,
    sliderDisplayValue, appUnitValue, appUnitIsValid, maxAppUnit, creditStep, formatAppUnit, clusterName, appId, dispatch,
    provAppCreditPlanned, provAppCpuCores, provAppMemory, provAppDiskSize,
    clusterCreditsAvailable, clusterCredits,
  ])

  return (
    <Flex direction="column" className={`${styles.tableSectionWrapper}`} w={"100%"} gap="8px">
      <TableType2 dataArray={dataObject} className={styles.table} />
      <CopyTextModal
        isOpen={isTemplatePreviewOpen}
        closeModal={() => setIsTemplatePreviewOpen(false)}
        title={templatePreviewTitle}
        text={templatePreviewContent}
        showPrettyJsonCheckbox={false}
      />
      <ConfirmModal
        isOpen={appUnitConfirmState.isOpen}
        closeModal={closeAppUnitConfirm}
        title={appUnitConfirmState.title}
        onConfirmClick={() => {
          appUnitConfirmState.handler?.()
          closeAppUnitConfirm()
        }}
      />
      <ConfirmModal
        isOpen={isResetImpactModalOpen}
        closeModal={() => setIsResetImpactModalOpen(false)}
        title={resetImpactForceRefresh ? 'Refresh and Apply Template' : 'Apply Template'}
        body={
          <Box>
            <Text mb={2}>Template: <strong>{resetImpactTemplate}</strong></Text>
            <Text mb={2}>Detected changes: <strong>{resetImpactChanges.length}</strong></Text>
            {resetImpactChanges.length > 0 && (
              <Box as='ul' pl={5} maxH='220px' overflowY='auto'>
                {resetImpactChanges.slice(0, 12).map((change) => (
                  <Box as='li' key={`${change.field}-${change.old}-${change.new}`}>
                    <Text as='span' fontWeight='600'>{change.field}</Text>
                    <Text as='span'>: {change.old || '(empty)'} → {change.new || '(empty)'}</Text>
                  </Box>
                ))}
                {resetImpactChanges.length > 12 && (
                  <Box as='li'>
                    <Text>... and {resetImpactChanges.length - 12} more change(s)</Text>
                  </Box>
                )}
              </Box>
            )}
          </Box>
        }
        onConfirmClick={onConfirmResetFromTemplate}
        confirmButtonText='Apply'
      />
    </Flex>
  )
}

export default React.memo(GeneralSection);

GeneralSection.propTypes = {
  clusterName: PropTypes.string.isRequired,
  appId: PropTypes.string.isRequired,
  appName: PropTypes.string,
  appHost: PropTypes.string,
  config: PropTypes.shape({
    provAppAgents: PropTypes.oneOfType([PropTypes.array, PropTypes.string]),
    provDbAgents: PropTypes.oneOfType([PropTypes.array, PropTypes.string]),
    provAppCpuCores: PropTypes.oneOfType([PropTypes.number, PropTypes.string]),
    provAppMemory: PropTypes.oneOfType([PropTypes.number, PropTypes.string]),
    provAppDiskSize: PropTypes.oneOfType([PropTypes.number, PropTypes.string]),
    provAppSizingMode: PropTypes.string,
    cloud18ApplicationCredits: PropTypes.number,
    cloud18ApplicationCreditsUsed: PropTypes.number,
  }),
  appConfig: PropTypes.shape({
    provAppDockerImg: PropTypes.string,
    provAppDockerCmd: PropTypes.string,
    provAppTemplate: PropTypes.string,
    provAppAgents: PropTypes.oneOfType([PropTypes.array, PropTypes.string]),
    provAppHaTopology: PropTypes.string,
    provAppCreditPlanned: PropTypes.number,
    provAppSizingMode: PropTypes.string,
    provAppCpuCores: PropTypes.oneOfType([PropTypes.number, PropTypes.string]),
    provAppMemory: PropTypes.oneOfType([PropTypes.number, PropTypes.string]),
    provAppDiskSize: PropTypes.oneOfType([PropTypes.number, PropTypes.string]),
  }).isRequired,
  dockerTemplates: PropTypes.arrayOf(PropTypes.string),
  substitution: PropTypes.array,
  user: PropTypes.shape({
    grants: PropTypes.object
  })
}
