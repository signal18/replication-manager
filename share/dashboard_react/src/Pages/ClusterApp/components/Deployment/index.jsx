
import { Heading, VStack, HStack, Flex } from "@chakra-ui/react";
import { useSelector } from "react-redux";
import styles from "./styles.module.scss";
import DeploymentDetail from "./details";
import AccordionComponent from "../../../../components/AccordionComponent";
import GeneralSection from "./GeneralSection";

const Deployment = ({ clusterName, config, appId, appConfig}) => {
    const deployment = useSelector((state) => state.cluster?.app?.deployment);

    return (
        <Flex direction="column" className={styles.contentContainer} w={"100%"} alignItems={"flex-start"} gap={4}>
            <HStack alignContent={"space-between"} w={"100%"}><Heading mb={4}>Deployment Details</Heading></HStack>
            <Flex direction="column" className={styles.sectionWrapper}>
                <VStack spacing={3} align="stretch">
                    <AccordionComponent
                        heading={'General Section'}
                        body={<GeneralSection clusterName={clusterName} appId={appId} config={config} appConfig={appConfig} />}
                    />
                </VStack>
            </Flex>
            <DeploymentDetail clusterName={clusterName} row={deployment} appId={appId} dockerImage={appConfig?.provAppDockerImg} agentList={appConfig?.provAppAgents} />
        </Flex>
    );
};

export default Deployment;
