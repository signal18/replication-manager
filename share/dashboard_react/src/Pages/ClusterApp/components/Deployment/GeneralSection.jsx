import { Flex, Text } from '@chakra-ui/react'
import TextForm from '../../../../components/TextForm';
import styles from './styles.module.scss';
import { useDispatch, useSelector } from 'react-redux';
import TableType2 from '../../../../components/TableType2';
import { setAppSetting } from '../../../../redux/settingsSlice';
import Checkboxes from '../../../../components/Checkboxes/Checkboxes';
import { useMemo } from 'react';
import Dropdown from '../../../../components/Dropdown';
import RMIconButton from '../../../../components/RMIconButton';
import { HiRefresh } from 'react-icons/hi';

export default function GeneralSection({ clusterName, appId, config, appConfig, dockerTemplates, user }) {

  const dispatch = useDispatch();

  const templateOptions = useMemo(() => ([{ name: 'Select Template', value: '' }, ...dockerTemplates?.map(item => ({ name: item, value: item }))]), [dockerTemplates?.length]);

  const dataObject = [
    {
      key: 'App Name',
      value: (
        <TextForm
          value={appConfig?.appName}
          confirmTitle="App Name Change"
          confirmBody='Are you sure you want to change "app-name" to: '
          onSave={(value) =>
            dispatch(setAppSetting({ clusterName: clusterName, appId: appId, setting: 'app-name', value: value }))
          }
        />
      )
    },
    {
      key: 'App Host',
      value: (
        <TextForm
          value={appConfig?.appHost}
          confirmTitle="App Host Change"
          confirmBody='Are you sure you want to change "app-host" to: '
          onSave={(value) =>
            dispatch(setAppSetting({ clusterName: clusterName, appId: appId, setting: 'app-host', value: value }))
          }
        />
      )
    },
    {
      key: 'Docker Image',
      value: (
        <TextForm
          value={appConfig?.provAppDockerImg}
          confirmTitle="Docker Image Change"
          confirmBody='Are you sure you want to change "prov-app-docker-img" to: '
          onSave={(value) =>
            dispatch(setAppSetting({ clusterName: clusterName, appId: appId, setting: 'prov-app-docker-img', value: value }))
          }
        />
      )
    },
    {
      key: 'Docker Template',
      value: (
        <Dropdown
          confirmTitle="Docker Template Change"
          confirmBody='Are you sure you want to change "prov-app-template" to: '
          isMenuPortalTarget={false}
          onChange={(option) => { setAppSetting({ clusterName: clusterName, appId: appId, setting: 'prov-app-template', value: option.value }) }}
          options={templateOptions}
          selectedValue={appConfig?.provAppTemplate}
        />
      )
    },
    {
      key: 'Reset App From Template',
      value: (
        <RMIconButton
          icon={HiRefresh}
          aria-label="Reset App From Template"
          tooltip="Reset App From Template"
          onClick={() => dispatch(resetAppFromTemplate({ clusterName, appId }))}
          isDisabled={!appConfig?.provAppTemplate}
          confirmTitle="Reset App From Template"
        />
      )
    },
    {
      key: 'OpenSVC Agents',
      value: (
        <Checkboxes
          list={config?.provAppAgents ? config?.provAppAgents : config?.provDbAgents}
          values={appConfig?.provAppAgents}
          confirm={true}
          splitConfirm={true}
          confirmTitle={`Confirm change 'prov-app-agents' to: `}
          onChange={(value) => dispatch(setAppSetting({ clusterName, appId, setting: 'prov-app-agents', value: value.toString() }))} // Convert array to string (auto join with comma)
          parentStyles={styles}
        />
      )
    },
  ];

  return (
    <Flex direction="column" className={`${styles.tableSectionWrapper}`} w={"100%"} gap="8px">
      <TableType2 dataArray={dataObject} className={styles.table} />
    </Flex>
  )
}