import { Flex } from '@chakra-ui/react'
import TextForm from '../../../../components/TextForm';
import styles from './styles.module.scss';
import { useDispatch, useSelector } from 'react-redux';
import TableType2 from '../../../../components/TableType2';
import { setAppSetting } from '../../../../redux/settingsSlice';
import Checkboxes from '../../../../components/Checkboxes/Checkboxes';



export default function GeneralSection({ clusterName, appId, config, appConfig }) {

  const dispatch = useDispatch();

  const dataObject = [
    {
      key: 'Docker Image',
      value: (
        <TextForm
          value={appConfig?.provAppDockerImg}
          confirmTitle={`Confirm change 'prov-app-docker-img' to: `}
          onSave={(value) =>
            dispatch(setAppSetting({ clusterName: clusterName, appId: appId, setting: 'prov-app-docker-img', value: value.length === 0 ? '{undefined}' : value }))
          }
        />
      )
    },
    {
      key: 'Container Host',
      value: (
        <TextForm
          value={appConfig?.appHost}
          confirmTitle={`Confirm change 'app-host' to: `}
          onSave={(value) =>
            dispatch(setAppSetting({ clusterName: clusterName, appId: appId, setting: 'app-host', value: value.length === 0 ? '{undefined}' : value }))
          }
        />
      )
    },
    {
      key: 'Container Port',
      value: (
        <TextForm
          value={appConfig?.appPort}
          confirmTitle={`Confirm change 'app-port' to: `}
          onSave={(value) =>
            dispatch(setAppSetting({ clusterName: clusterName, appId: appId, setting: 'app-port', value: value.length === 0 ? '{undefined}' : value }))
          }
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
    <Flex direction="column" className={`${styles.sectionWrapper}`} w={"100%"} gap="8px">
      <TableType2 dataArray={dataObject} className={styles.table} />
    </Flex>
  )
}