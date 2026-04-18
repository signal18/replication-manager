import { Box, Flex, Text } from '@chakra-ui/react'
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
  const { provAppDockerImg = '', provAppDockerCmd = '', provAppTemplate = '', provAppAgents = '', provAppHaTopology = '' } = appConfig;
  const agentList = config?.provAppAgents ? config?.provAppAgents : config?.provDbAgents;
  const onSaveDockerImage = useCallback((value) => dispatch(setAppSetting({ clusterName: clusterName, appId: appId, setting: 'prov-app-docker-img', value: value })), [clusterName, appId, dispatch])
  const onSaveDockerCmd = useCallback((value) => dispatch(setAppSetting({ clusterName: clusterName, appId: appId, setting: 'prov-app-docker-cmd', value: value })), [clusterName, appId, dispatch])
  const onSaveAppAsTemplate = useCallback(() => dispatch(saveAppAsTemplate({ clusterName: clusterName, appId: appId, template: appName })), [clusterName, appId, appName, dispatch])
  const onResetAppFromTemplate = useCallback((value) => {
    if (!value) return
    dispatch(previewResetAppTemplateImpact({ clusterName, appId, template: value, forceRefresh: false }))
      .unwrap()
      .then(({ data }) => {
        setResetImpactTemplate(value)
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
  const onAgentsChange = useCallback((value) => dispatch(setAppSetting({ clusterName, appId, setting: 'prov-app-agents', value: value.toString() })), [clusterName, appId, dispatch])
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

  const dataObject = useMemo(() => {
    return [
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
            isDisabled={!user?.grants['app-deployment']}
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
            confirmTitle={`Confirm change 'prov-app-agents' to: `}
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
  }, [appName, appHost, provAppDockerImg, onSaveDockerImage, provAppDockerCmd, onSaveDockerCmd, onSaveAppAsTemplate, templateOptions, provAppTemplate, onPreviewTemplate, onResetAppFromTemplate, onRefreshAndResetAppFromTemplate, agentList, onAgentsChange, provAppAgents, onHATopologyChange, provAppHaTopology, haTopologyOptions, substitution, user])
  
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
    provAppAgents: PropTypes.array,
    provDbAgents: PropTypes.array
  }),
  appConfig: PropTypes.shape({
    provAppDockerImg: PropTypes.string,
    provAppDockerCmd: PropTypes.string,
    provAppTemplate: PropTypes.string,
    provAppAgents: PropTypes.oneOfType([PropTypes.array, PropTypes.string]),
    provAppHaTopology: PropTypes.string
  }).isRequired,
  dockerTemplates: PropTypes.arrayOf(PropTypes.string),
  substitution: PropTypes.array,
  user: PropTypes.shape({
    grants: PropTypes.object
  })
}
