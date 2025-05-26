import { useState, useEffect, useMemo } from "react";
import { Table, Thead, Tbody, Tr, Th, Td, Heading, Tag, Button, Stack, VStack, Flex, HStack, Box } from "@chakra-ui/react";
import { useDispatch, useSelector } from "react-redux";
import RMButton from "../../../../components/RMButton";
import DeploymentFormModal from "../../../../components/Modals/AppDeploymentModal";
import styles from "./styles.module.scss";
import { addDeployment } from "../../../../redux/clusterSlice";
import { createColumnHelper } from "@tanstack/react-table";
import RMIconButton from "../../../../components/RMIconButton";
import { TbEye, TbTrash } from "react-icons/tb";
import { DataTable } from "../../../../components/DataTable";
import DeploymentDetail from "./details";

const Deployments = ({ clusterName, selectedApp }) => {
    const dispatch = useDispatch()
    const [isFormOpen, setIsFormOpen] = useState(false);
    const [isDetailsOpen, setIsDetailsOpen] = useState(false);
    const [initValues, setInitValues] = useState(null)

    const {
        cluster: { app },
    } = useSelector((state) => state)

    const openDetails = () => {
        setIsDetailsOpen(true)
    }

    const closeDetails = () => {
        setIsDetailsOpen(false)
    }

    const openModal = () => {
        setIsFormOpen(true)
    }

    const closeModal = () => {
        setIsFormOpen(false)
    }

    const handleSubmit = (deployment) => {
        if (initValues === null) {
            dispatch(addDeployment({ clusterName: clusterName, appId: selectedApp.id, deployment }))
        } 
    }

    const columnHelper = createColumnHelper()
    const columns = useMemo(
        () => [
            columnHelper.accessor((row) => row.name, {
                cell: (info) => info.getValue(),
                header: 'Name'
            }),
            columnHelper.accessor((row) => row.dockerImg, {
                cell: (info) => info.getValue(),
                header: 'Docker Image'
            }),
            columnHelper.accessor((row) => (row.ports?.map((port, idx) => (<Tag key={idx} colorScheme="blue">{`${port}`}</Tag>))), {
                    cell: (info) => info.getValue(),
                    header: 'Ports'
            }),
            columnHelper.accessor((row) => (row.gitClones?.map((git, idx) => (<Tag key={idx} colorScheme="green">{`${git.repo} (${git.branch}) → ${git.dest}`}</Tag>))), {
                    cell: (info) => info.getValue(),
                    header: 'Git Clones'
            }),
            columnHelper.accessor((row) => (<HStack gap={2}><RMIconButton icon={TbEye} tooltip="Details" onClick={() => { setInitValues(row); openDetails() }} /><RMIconButton icon={TbTrash} tooltip="Delete" onClick={() => {}} /></HStack>), {
                cell: (info) => info.getValue(),
                header: 'Actions'
            }),
        ],
        [app.deployments]
    )

    return (
        <>
            { !isDetailsOpen ? (
            <VStack className={styles.contentContainer}>
                <HStack alignContent={"space-between"} w={"100%"}><Heading mb={4}>Deployments Overview</Heading><RMButton ml={"auto"} onClick={openModal}>Add</RMButton></HStack>
                <Flex className={styles.tableContainer}>
                        <DataTable data={app.deployments || []} columns={columns} />
                </Flex>
            </VStack>
            ) : (
            <VStack className={styles.contentContainer} >
                <HStack alignContent={"space-between"} w={"100%"}><Heading mb={4}>Deployment Details: {initValues.name}</Heading><RMButton ml={"auto"} onClick={closeDetails}>Back</RMButton></HStack>
                <Flex className={styles.tableContainer}>
                    <DeploymentDetail clusterName={clusterName} row={initValues} appId={app.name} deployId={initValues.name} />
                </Flex>
            </VStack>
            ) }
            {isFormOpen && <DeploymentFormModal isOpen={isFormOpen} onClose={closeModal} onSubmit={handleSubmit} />}
        </>
    );
};

export default Deployments;
