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


const defaultVol = { name: "", poolname: "", volumedir: "" };

const volumeDirs = ["etc", "var", "log"].map((dir) => ({ value: dir, name: dir }));

const columnHelper = createColumnHelper()

const maskString = (str, mask = '*') => {
    return str.replaceAll(/./g, mask)
}

const VolumeSection = ({
    rows = [],
    opensvcPools = [],
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

    const poolOptions = useMemo(() => {
        if (!Array.isArray(opensvcPools) || opensvcPools.length === 0) {
            return [];
        }

        return [...new Set(opensvcPools.filter(Boolean))]
            .sort((a, b) => a.localeCompare(b))
            .map((poolName) => ({
                value: poolName,
                name: poolName,
            }));
    }, [opensvcPools]);

    const handleAddItem = () => {
        setIsVisible(true);
        onPauseAutoReload(); // Pause auto-reload when adding a new item
    };

    const handleCancel = () => {
        setIsVisible(false); // Hide the form without saving
        onResumeAutoReload(); // Resume auto-reload after canceling
    };

    const handleSaveAdd = (formData) => {
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
            columnHelper.accessor((row) => row.poolname, {
                header: 'Pool Name'
            }),
            columnHelper.accessor((row) => row.volumedir, {
                header: 'Volume Dir'
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
                        return (<VolumeRowForm poolOptions={poolOptions} fieldName={fieldName} volume={row.original} index={row.index} onChange={onRowArrayChange} />);
                    },
                },
                cell: () => null,
            }
        ],
        [fieldName, onRowArrayChange, onRowDropIndex, poolOptions]
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
                        <VolumeNewForm onSave={handleSaveAdd} onCancel={handleCancel} saveCaption={saveCaption} poolOptions={poolOptions} />
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

const VolumeRowForm = React.memo(({ fieldName, volume, index, poolOptions = [], onChange }) => {
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
                    <Text mb={1}>Pool Name:</Text>
                    <Dropdown placeholder="Pool Name" confirmTitle="Change Pool Name" options={poolOptions} selectedValue={vol.poolname} onChange={(value) => onRowArrayChange(fieldName, index, "poolname", value)} />
                </Flex>
                <Flex direction="column" flex="1">
                    <Text mb={1}>Volume Dir:</Text>
                    <TextForm placeholder="Volume Dir" confirmTitle="Change Volume Dir" value={vol.volumedir} onSave={(value) => onRowArrayChange(fieldName, index, "volumedir", value)} />
                </Flex>
            </Flex>
        </Flex>
    )
})

const VolumeNewForm = React.memo(({ saveCaption = "Save Volume", onSave = () => { }, poolOptions = [], onCancel = () => { } }) => {
    const [vol, setVol] = useState(defaultVol);

    const valid = useMemo(() => {
        return vol.name && vol.poolname && vol.volumedir;
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
                    <Text mb={1}>Pool Name:</Text>
                    <Dropdown placeholder="Pool Name" options={poolOptions} selectedValue={vol.poolname} onChange={(option) => handleArrayChange("poolname", option.value)} />
                </Flex>
                <Flex direction="column" flex="1">
                    <Text mb={1}>Volume Dir:</Text>
                    <Input placeholder="Volume Dir" value={vol.volumedir} onChange={(e) => handleArrayChange("volumedir", e.target.value)} />
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
