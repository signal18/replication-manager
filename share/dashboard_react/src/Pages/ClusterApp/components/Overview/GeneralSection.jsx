import { Flex, Text } from '@chakra-ui/react'
import TextForm from '../../../../components/TextForm';
import styles from './styles.module.scss';
import { useDispatch } from 'react-redux';
import TableType2 from '../../../../components/TableType2';
import { resetAppFromTemplate, saveAppAsTemplate, setAppSetting } from '../../../../redux/settingsSlice';
import Checkboxes from '../../../../components/Checkboxes/Checkboxes';
import React, { useMemo } from 'react';
import Dropdown from '../../../../components/Dropdown';
import RMIconButton from '../../../../components/RMIconButton';
import { AiOutlineSave } from 'react-icons/ai';
import VariableInputArea from '../../../../components/VariableTree/VariableInputArea';

export default React.memo(function GeneralSection({ clusterName, appId, appName, appHost, config, appConfig, dockerTemplates, substitution, user }) {

  const dispatch = useDispatch();
  const haTopologyOptions = useMemo(() => ([{ value: 'failover', name: 'Failover' }, { value: 'flex', name: 'Flex' }]), []);
  const templateOptions = useMemo(() => ([{ name: 'Select Template', value: '' }, ...dockerTemplates?.map(item => ({ name: item, value: item }))]), [dockerTemplates?.length]);
  const { provAppDockerImg = '', provAppDockerCmd = '', provAppTemplate = '', provAppAgents = '', provAppHaTopology = '' } = appConfig;
  const agentList = config?.provAppAgents ? config?.provAppAgents : config?.provDbAgents;
  const onSaveDockerImage = (value) => dispatch(setAppSetting({ clusterName: clusterName, appId: appId, setting: 'prov-app-docker-img', value: value }))
  const onSaveDockerCmd = (value) => dispatch(setAppSetting({ clusterName: clusterName, appId: appId, setting: 'prov-app-docker-cmd', value: value }))
  const onSaveAppAsTemplate = () => dispatch(saveAppAsTemplate({ clusterName: clusterName, appId: appId, template: appName }))
  const onResetAppFromTemplate = (value) => dispatch(resetAppFromTemplate({ clusterName, appId, template: value }))
  const onAgentsChange = (value) => dispatch(setAppSetting({ clusterName, appId, setting: 'prov-app-agents', value: value.toString() }))
  const onHATopologyChange = (value) => { dispatch(setAppSetting({ clusterName: clusterName, appId: appId, setting: 'prov-app-ha-topology', value: value })) }

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
        value: (<Text>{provAppTemplate}</Text>)
      },
      {
        key: 'Reset App From Template',
        value: (
          <Dropdown
            confirmTitle="Docker Template Change"
            confirmBody='Are you sure you want to reset template using: '
            isMenuPortalTarget={true}
            onChange={onResetAppFromTemplate}
            selectedValue={provAppTemplate}
            options={templateOptions}
          />
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
  }, [appName, appHost, provAppDockerImg, onSaveDockerImage, provAppDockerCmd, onSaveDockerCmd, onSaveAppAsTemplate, templateOptions, provAppTemplate, onResetAppFromTemplate, agentList, onAgentsChange, provAppAgents, onHATopologyChange, provAppHaTopology, haTopologyOptions])
  
  return (
    <Flex direction="column" className={`${styles.tableSectionWrapper}`} w={"100%"} gap="8px">
      <TableType2 dataArray={dataObject} className={styles.table} />
    </Flex>
  )
})