import { useState, useEffect } from "react";
import { Table, Thead, Tbody, Tr, Th, Td, Heading, Tag, Button, Stack, VStack, Flex, HStack } from "@chakra-ui/react";
import { useDispatch, useSelector } from "react-redux";
import RMButton from "../../../../components/RMButton";
import DeploymentFormModal from "../../../../components/Modals/AppDeploymentModal";
import styles from "./styles.module.scss";
import { addDeployment } from "../../../../redux/clusterSlice";

const Deployments = ({selectedApp}) => {
    const dispatch = useDispatch()
    const [deployments, setDeployments] = useState([]);
    const [isFormOpen, setIsFormOpen] = useState(false);

    const {
        cluster: { selectedCluster, app },
    } = useSelector((state) => state)

    const openModal = () => {
        setIsFormOpen(true)
    }

    const closeModal = () => {
        setIsFormOpen(false)
    }

    const handleSubmit = (deployment) => {
        dispatch(addDeployment({ clusterName: selectedCluster?.name, appId: selectedApp.id , deployment}))
    }

    // Clear the state when the component is unmounted
    useEffect(() => {
        return () => {
            setDeployments([])
        }
    }, [])

    useEffect(() => {
        if (app.deployments) {
            setDeployments(app.deployments)
        }
    }, [app.deployments])

    return (
        <>
            <VStack className={styles.contentContainer}>
                <HStack className={styles.actions} alignContent={"space-between"}><Heading mb={4}>Deployments Overview</Heading><RMButton onClick={openModal}>Add Deployment</RMButton></HStack>
                <Flex className={styles.actions}>
                    <Table variant="simple" w={["100%", "100%", "100%", "100%"]}>
                        <Thead>
                            <Tr>
                                <Th>Name</Th>
                                <Th>Docker Image</Th>
                                <Th>Ports</Th>
                                <Th>Git Clones</Th>
                                <Th>Actions</Th>
                            </Tr>
                        </Thead>
                        <Tbody>
                            {deployments.map((deployment, index) => (
                                <Tr key={index}>
                                    <Td>{deployment.name}</Td>
                                    <Td>{deployment.dockerImg}</Td>
                                    <Td>
                                        <Stack spacing={1}>
                                            {deployment.ports.map((port, idx) => (
                                                <Tag key={idx} colorScheme="blue">{`${port.containerPort}/${port.protocol}`}</Tag>
                                            ))}
                                        </Stack>
                                    </Td>
                                    <Td>
                                        <Stack spacing={1}>
                                            {deployment.gitClones.map((git, idx) => (
                                                <Tag key={idx} colorScheme="green">{`${git.repo} (${git.branch}) → ${git.dest}`}</Tag>
                                            ))}
                                        </Stack>
                                    </Td>
                                    <Td>
                                        <Button size="sm" colorScheme="blue">View</Button>
                                        <Button size="sm" colorScheme="red" ml={2}>Delete</Button>
                                    </Td>
                                </Tr>
                            ))}
                        </Tbody>
                    </Table>
                </Flex>
            </VStack>
            {isFormOpen && <DeploymentFormModal  isOpen={isFormOpen} onClose={closeModal} onSubmit={handleSubmit} />}
        </>
    );
};

export default Deployments;
