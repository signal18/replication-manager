import { useState, useEffect, useMemo } from "react";
import { Table, Thead, Tbody, Tr, Th, Td, Heading, Tag, Button, Stack, VStack, Flex, HStack, Box } from "@chakra-ui/react";
import { useDispatch, useSelector } from "react-redux";
import RMButton from "../../../../components/RMButton";
import DeploymentFormModal from "../../../../components/Modals/AppDeploymentModal";
import styles from "./styles.module.scss";
import { addDeployment } from "../../../../redux/clusterSlice";
import { createColumnHelper } from "@tanstack/react-table";
import RMIconButton from "../../../../components/RMIconButton";
import { TbEdit, TbTrash } from "react-icons/tb";
import { DataTable } from "../../../../components/DataTable";

const Deployments = ({ clusterName, selectedApp }) => {
    const dispatch = useDispatch()
    const [isFormOpen, setIsFormOpen] = useState(false);
    const [initValues, setInitValues] = useState(null)

    const {
        cluster: { app },
    } = useSelector((state) => state)

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
            columnHelper.accessor((row) => (row.ports.map((port, idx) => (<Tag key={idx} colorScheme="blue">{`${port}`}</Tag>))), {
                    cell: (info) => info.getValue(),
                    header: 'Ports'
            }),
            columnHelper.accessor((row) => (row.gitClones.map((git, idx) => (<Tag key={idx} colorScheme="green">{`${git.repo} (${git.branch}) → ${git.dest}`}</Tag>))), {
                    cell: (info) => info.getValue(),
                    header: 'Git Clones'
            }),
            columnHelper.accessor((row) => (<><RMIconButton icon={TbEdit} tooltip="Edit" onClick={() => { setInitValues(row); openModal() }} /><RMIconButton icon={TbTrash} tooltip="Delete" onClick={() => {}} /></>), {
                cell: (info) => info.getValue(),
                header: 'Actions'
            }),
        ],
        [app.deployments]
    )

    return (
        <>
            <VStack className={styles.contentContainer}>
                <HStack className={styles.actions} alignContent={"space-between"}><Heading mb={4}>Deployments Overview</Heading><RMButton onClick={openModal}>Add Deployment</RMButton></HStack>
                <Flex className={styles.tableContainer}>
                        <DataTable data={app.deployments || []} columns={columns} />
                </Flex>
            </VStack>
            {isFormOpen && <DeploymentFormModal initialValues={initValues} isOpen={isFormOpen} onClose={closeModal} onSubmit={handleSubmit} />}
        </>
    );
};

export default Deployments;
