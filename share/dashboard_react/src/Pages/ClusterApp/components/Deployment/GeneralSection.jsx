import { Flex } from '@chakra-ui/react'
import TextForm from '../../../../components/TextForm';
import styles from './styles.module.scss';
import { useDispatch } from 'react-redux';
import TableType2 from '../../../../components/TableType2';
import { setAppSetting } from '../../../../redux/settingsSlice';



export default function GeneralSection({ clusterName, appId, config }) {

  const dispatch = useDispatch();
  
  const dataObject = [
  {
      key: 'Docker Image',
      value: (
        <TextForm
          value={config?.provAppDockerImg}
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
          value={config?.appHost}
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
          value={config?.appPort}
          confirmTitle={`Confirm change 'app-port' to: `}
          onSave={(value) =>
            dispatch(setAppSetting({ clusterName: clusterName, appId: appId, setting: 'app-port', value: value.length === 0 ? '{undefined}' : value }))
          }
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
