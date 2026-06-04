
import { Heading, VStack, HStack, Flex, Text } from "@chakra-ui/react";
import { useDispatch, useSelector } from "react-redux";
import styles from "./styles.module.scss";
import AccordionComponent from "../../../../components/AccordionComponent";
import GeneralSection from "./GeneralSection";
import AppCredit from "./AppCredit";
import { useCallback, useMemo, useState } from "react";
import { deploymentFieldChange, deploymentFieldIndexAdd, deploymentFieldIndexDrop, pauseAutoReload, resolveTemplateVariables } from "../../../../redux/clusterSlice";
import ConfirmModal from "../../../../components/Modals/ConfirmModal";
import Routes from "./Routes";
import PathSection from "./Paths";
import Variables from "./Variables";
import PropTypes from "prop-types";

const Overview = ({ clusterName, config, appId, appName, appHost, appConfig, user }) => {
    const dispatch = useDispatch();
    const deployment = useSelector((state) => state.cluster?.app?.deployment);
    const substitution = useSelector((state) => state.cluster?.app?.substitution);
    const dockerTemplates = useSelector((state) => state.globalClusters.monitor?.serviceTemplates);
    
    const [modalState, setModalState] = useState({
        isOpen: false,
        field: null,
        index: null,
    })
    const {isOpen: isConfirmOpen, field, index} = modalState;

    const dropConfirmText = useMemo(() => field
        ? `Are you sure you want to remove this ${field} item? This action cannot be undone.`
        : "Are you sure you want to remove this item?", [field]);

    const handleSaveArrayChange = useCallback(
        (field, index, key, value) => dispatch(deploymentFieldChange({ clusterName, appId, field, index, key, value })),
        [clusterName, appId, dispatch]
    )

    const handleSaveAddItem = useCallback(
        (field, value) => dispatch(deploymentFieldIndexAdd({ clusterName, appId, field, value })),
        [clusterName, appId, dispatch]
    )

    const handleDropIndex = useCallback(
        (field, index) => {
            setModalState((prev) => ({ ...prev, field, index, isOpen: true }))
        },
        []
    )

    const handlePauseAutoReload = useCallback(
        () => dispatch(pauseAutoReload({ isPaused: true })),
        [dispatch]
    )

    const handleResumeAutoReload = useCallback(
        () => dispatch(pauseAutoReload({ isPaused: false })),
        [dispatch]
    )

    const handleResolveVariables = useCallback(
        (rawValue) => {
            console.log("Resolving variables for:", rawValue); 
            return dispatch(resolveTemplateVariables({ clusterName, appId, rawValue }))
        },
        [clusterName, appId, dispatch]
    )

    const actionProps = useMemo(() => ({
        onRowArrayChange: handleSaveArrayChange,
        onSaveAdd: handleSaveAddItem,
        onRowDropIndex: handleDropIndex,
        onPauseAutoReload: handlePauseAutoReload,
        onResumeAutoReload: handleResumeAutoReload,
    }), [handleSaveArrayChange, handleSaveAddItem, handleDropIndex, handlePauseAutoReload, handleResumeAutoReload]);

    const handleCloseConfirm = useCallback(() => {
        setModalState({ isOpen: false, field: null, index: null });
      }, []);
    
      const handleConfirmDrop = useCallback(() => {
        if (field && typeof index === 'number') {
          dispatch(deploymentFieldIndexDrop({ clusterName, appId, field, index }));
          handleCloseConfirm();
        }
      }, [clusterName, appId, field, index, dispatch, handleCloseConfirm]);

    const routes = useMemo(() => deployment?.routes || [], [deployment]);
    const storages = useMemo(() => deployment?.storages || {}, [deployment]);
    const gitClones = useMemo(() => storages?.gitClones || [], [storages]);
    const paths = useMemo(() => deployment?.paths || [], [deployment]);
    const variables = useMemo(() => deployment?.variables || [], [deployment]);
    const dockerImage = useMemo(() => appConfig?.provAppDockerImg || '', [appConfig]);
    const agentList = useMemo(() => appConfig?.provAppAgents || [], [appConfig]);
    const gateway = useMemo(() => config?.cloud18GatewayDomainName || '', [config]);

    const routeComponent = useMemo(() => {
        return <Routes rows={routes} variables={variables} fieldName={'routes'} user={user} gateway={gateway} {...actionProps} />
    }, [routes, variables, actionProps, user, gateway]);

    const pathComponent = useMemo(() => {
        return <PathSection storages={storages} rows={paths} fieldName={'paths'} clusterName={clusterName} appId={appId} dockerImage={dockerImage} gitCloneRows={gitClones} user={user} {...actionProps} />;
    }, [paths, clusterName, appId, dockerImage, gitClones, actionProps, storages, user]);

    const variableComponent = useMemo(() => {
        return <Variables substitution={substitution} rows={variables} agentList={agentList} fieldName={'variables'} user={user} onResolveVariable={handleResolveVariables} {...actionProps} />;
    }, [variables, substitution, agentList, actionProps, handleResolveVariables, user]);

    const GeneralSectionComponent = useMemo(() => {
        return <GeneralSection clusterName={clusterName} appId={appId} appName={appName} appHost={appHost} config={config} appConfig={appConfig} dockerTemplates={dockerTemplates} substitution={substitution} user={user} />;
    }, [clusterName, appId, appName, appHost, config, appConfig, dockerTemplates, substitution, user]);

    if (!appConfig) {
        return (<Text>Waiting for config</Text>)
    }

    return (
        <Flex direction="column" className={styles.contentContainer} w={"100%"} alignItems={"flex-start"} gap={4}>
            <HStack alignContent={"space-between"} w={"100%"}><Heading mb={4}>Deployment Details</Heading></HStack>
            <Flex direction="column" className={styles.sectionWrapper}>
                <VStack spacing={3} align="stretch">
                    <AccordionComponent
                        heading={'General Section'}
                        body={GeneralSectionComponent}
                    />
                    <AccordionComponent
                        heading={'Infra Resources'}
                        body={<AppCredit clusterName={clusterName} appId={appId} config={config} appConfig={appConfig} user={user} />}
                    />
                    <AccordionComponent
                        heading={'DNS Routes'}
                        body={routeComponent}
                    />
                    <AccordionComponent
                        heading={'ENV Variables'}
                        body={variableComponent}
                    />
                    <AccordionComponent
                        heading={'Storage Mappings'}
                        body={pathComponent}
                    />
                    <ConfirmModal
                        isOpen={isConfirmOpen}
                        closeModal={handleCloseConfirm}
                        onConfirmClick={handleConfirmDrop}
                        title="Confirm Delete"
                        body={dropConfirmText}
                    />
                </VStack>
            </Flex>
        </Flex>
    );
};

Overview.propTypes = {
    clusterName: PropTypes.string.isRequired,
    config: PropTypes.shape({
        cloud18GatewayDomainName: PropTypes.string,
    }).isRequired,
    appId: PropTypes.string.isRequired,
    appName: PropTypes.string,
    appHost: PropTypes.string,
    appConfig: PropTypes.shape({
        provAppDockerImg: PropTypes.string,
        provAppAgents: PropTypes.oneOfType([PropTypes.arrayOf(PropTypes.string), PropTypes.string]),
    }).isRequired,
    user: PropTypes.shape({
        grants: PropTypes.object,
    }),
};

export default Overview;
