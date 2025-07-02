import React, { useMemo, useState } from "react";
import { VStack, Input, HStack, Heading, Flex, Select, Box, Text } from "@chakra-ui/react";
import styles from "./styles.module.scss";
import TextForm from "../../../../components/TextForm";
import { createColumnHelper } from "@tanstack/react-table";
import RMButton from "../../../../components/RMButton";
import RMIconButton from "../../../../components/RMIconButton";
import Dropdown from "../../../../components/Dropdown";
import { DataTable } from "../../../../components/DataTable";
import { HiTrash } from "react-icons/hi";


const defaultVol = { name: "", volumename: "", volumedir: "" };

const volumeDirs = ["etc", "var", "log"].map((dir) => ({ value: dir, name: dir }));

const columnHelper = createColumnHelper()

const maskString = (str, mask = '*') => {
    return str.replaceAll(/./g, mask)
}

const VolumeSection = ({
    rows = [],
    fieldName = "volumes",
    title = "Saved Volumes",
    newTitle = "Add New Volume",
    addCaption = "Add Volume",
    saveCaption = "Save Volume",
    onRowArrayChange,
    onRowDropIndex,
    onSaveAdd,
    onPauseAutoReload = () => { },
    onResumeAutoReload = () => { },
}) => {
    const [isVisible, setIsVisible] = useState(false);

    const handleAddItem = () => {
        setIsVisible(true);
        onPauseAutoReload(); // Pause auto-reload when adding a new item
    };

    const handleCancel = () => {
        setIsVisible(false); // Hide the form without saving
        onResumeAutoReload(); // Resume auto-reload after canceling
    };

    const handleSaveAdd = () => {
      onSaveAdd(fieldName, formData).then(() => {
        setIsVisible(false); // Hide the form after saving
        onResumeAutoReload(); // Resume auto-reload after saving
        return Promise.resolve();
      }, (error) => {
        return Promise.reject(error);
      });
  }

    const columnsRowForm = useMemo(
        () => [
            columnHelper.accessor((row) => row.name, {
                header: 'Name'
            }),
            columnHelper.accessor((row) => row.volumename, {
                header: 'Volume Name',
            }),
            columnHelper.accessor((row) => row.volumedir, {
                header: 'Volume Dir',
            }),
            columnHelper.display({
                id: 'actions',
                cell: ({ row }) => (
                    <RMIconButton
                        icon={HiTrash}
                        aria-label="Delete Variable"
                        onClick={() => onRowDropIndex(fieldName, row.index)}
                    />
                ),
            }),
            {
                id: 'expansion',
                header: '',
                meta: {
                    renderExpansion: (row) => {
                        return (<VolumeRowForm fieldName={fieldName} volume={row.original} index={row.index} onChange={onRowArrayChange} />);
                    },
                },
                cell: () => null,
            }
        ],
        [fieldName, onRowArrayChange, onRowDropIndex]
    )

    return (
        <Flex direction="column" className={`${styles.sectionWrapper}`}>
            <VStack spacing={3} align="stretch">
                <Heading as="h3" size="md">
                    {title}
                </Heading>
                <Box className={styles.tableContainer}>
                    <DataTable key="app-variables" data={rows} columns={columnsRowForm} className={styles.table} enableExpanding={true} enableExpandingNoSubRows={true} />
                </Box>
            </VStack>
            {isVisible ? (
                <VStack spacing={3} align="stretch">
                    <Heading as="h3" size="md">
                        {newTitle}
                    </Heading>
                    <Box className={styles.tableContainer}>
                        <VolumeNewForm onSave={handleSaveAdd} onCancel={handleCancel} saveCaption={saveCaption} />
                    </Box>
                </VStack>
            ) : (
                <VStack spacing={3} align="stretch">
                    <HStack>
                        <RMButton onClick={handleAddItem}>
                            {addCaption}
                        </RMButton>
                    </HStack>
                </VStack>
            )}
        </Flex>
    )
}

export default React.memo(VolumeSection);

const VolumeRowForm = React.memo(({ fieldName, volume, index, onChange }) => {
    const vol = volume || defaultVol;

    const onRowArrayChange = (fieldName, index, key, value) => {
        onChange(fieldName, index, key, value);
    };

    return (
        <Flex className={styles.variableRowForm} w="100%" align="flex-start" gap={4}>
            <Flex direction="column" flex="1" minW="300px" gap={2}>
                <Flex direction="column" flex="1">
                    <Text mb={1}>Name:</Text>
                    <TextForm placeholder="Name" value={vol.name} onSave={(value) => onRowArrayChange(fieldName, index, "name", value)} />
                </Flex>
                <Flex direction="column" flex="1">
                    <Text mb={1}>Volume Name:</Text>
                    <TextForm placeholder="Volume Name" value={vol.volumename} onSave={(value) => onRowArrayChange(fieldName, index, "volumename", value)} />
                </Flex>
                <Flex direction="column" flex="1">
                    <Text mb={1}>Volume Dir:</Text>
                    <Dropdown placeholder="Volume Dir" confirmTitle="Change Volume Dir" options={volumeDirs} value={vol.volumedir} onChange={(value) => onRowArrayChange(fieldName, index, "volumedir", value)} />
                </Flex>
            </Flex>
        </Flex>
    )
})

const VolumeNewForm = React.memo(({ saveCaption = "Save Volume", onSave = () => { }, onCancel = () => { } }) => {
    const [vol, setVol] = useState(defaultVol);

    const valid = useMemo(() => {
        return vol.name && vol.repo && vol.branch && vol.volumedir;
    }, [vol]);

    const handleArrayChange = (key, value) => {
        setVol((prev) => ({ ...prev, [key]: value }));
    }

    const handleSaveAdd = () => {
        if (valid) {
            onSave(vol)
        }
    };

    const handleCancel = () => {
        setVol(defaultVol); // Reset form on cancel
        onCancel();
    };

    return (
        <Flex className={styles.VolumeRowForm} w="100%" align="flex-start" gap={4}>
            <Flex direction="column" flex="1" minW="300px" gap={2}>
                <Flex direction="column" flex="1">
                    <Text mb={1}>Name:</Text>
                    <Input placeholder="Name" value={vol.name} onChange={(e) => handleArrayChange("name", e.target.value)} />
                </Flex>
                <Flex direction="column" flex="1">
                    <Text mb={1}>Volume Name:</Text>
                    <Input placeholder="Volume Name" value={vol.volumename} onChange={(e) => handleArrayChange("volumename", e.target.value)} />
                </Flex>
                <Flex direction="column" flex="1">
                    <Text mb={1}>Volume Dir:</Text>
                    <Dropdown placeholder="Volume Dir" options={volumeDirs} value={vol.volumedir} onChange={(option) => handleArrayChange("volumedir", option.value)} />
                </Flex>
                <Flex direction="column" flex="1">
                    <HStack spacing={2} mt={4}>
                        <RMButton onClick={handleCancel}>
                            Clear Form
                        </RMButton>
                        <RMButton onClick={handleSaveAdd} isDisabled={!valid}>
                            {saveCaption}
                        </RMButton>
                    </HStack>
                </Flex>
            </Flex>
        </Flex>
    )
})