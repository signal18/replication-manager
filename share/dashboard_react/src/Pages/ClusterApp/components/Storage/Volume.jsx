import React, { useMemo, useState } from "react";
import { VStack, Input, HStack, Heading, Flex, Box, Text } from "@chakra-ui/react";
import styles from "./styles.module.scss";
import TextForm from "../../../../components/TextForm";
import { createColumnHelper } from "@tanstack/react-table";
import RMButton from "../../../../components/RMButton";
import RMIconButton from "../../../../components/RMIconButton";
import Dropdown from "../../../../components/Dropdown";
import { DataTable } from "../../../../components/DataTable";
import { HiTrash } from "react-icons/hi";


const defaultVol = { name: "", poolname: "", volumedir: "" };

const columnHelper = createColumnHelper()

const VolumeSection = ({
    rows = [],
    opensvcPools = [],
    appHaTopology = '',
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

        const normalized = opensvcPools
            .map((pool) => {
                if (typeof pool === 'string') {
                    const value = pool.trim();
                    return value ? { value, name: value, shared: false } : null;
                }

                if (pool && typeof pool === 'object') {
                    const value = String(pool.value ?? pool.name ?? '').trim();
                    if (!value) {
                        return null;
                    }
                    const name = String(pool.name ?? value).trim() || value;
                    return { value, name, shared: Boolean(pool.shared) };
                }

                return null;
            })
            .filter(Boolean);

        const dedup = new Map();
        for (const option of normalized) {
            if (!dedup.has(option.value)) {
                dedup.set(option.value, option);
            } else if (option.shared) {
                const current = dedup.get(option.value);
                dedup.set(option.value, { ...current, shared: true });
            }
        }

        let options = [...dedup.values()];
        if (appHaTopology === 'failover') {
            options = options.filter((option) => option.shared);
        }

        return options.sort((a, b) => a.name.localeCompare(b.name));
    }, [opensvcPools, appHaTopology]);

    const handleAddItem = () => {
        setIsVisible(true);
        onPauseAutoReload();
    };

    const handleCancel = () => {
        setIsVisible(false);
        onResumeAutoReload();
    };

    const handleSaveAdd = (formData) => {
      onSaveAdd(fieldName, formData).then(() => {
        setIsVisible(false);
        onResumeAutoReload();
      }).catch(() => {
        onResumeAutoReload();
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

    return (
        <Flex className={styles.variableRowForm} w="100%" align="flex-start" gap={4}>
            <Flex direction="column" flex="1" minW="300px" gap={2}>
                <Flex direction="column" flex="1">
                    <Text mb={1}>Name:</Text>
                    <TextForm placeholder="Name" value={vol.name} onSave={(value) => onChange(fieldName, index, "name", value)} />
                </Flex>
                <Flex direction="column" flex="1">
                    <Text mb={1}>Pool Name:</Text>
                    <Dropdown placeholder="Pool Name" confirmTitle="Change Pool Name" options={poolOptions} selectedValue={vol.poolname} onChange={(value) => onChange(fieldName, index, "poolname", value)} />
                </Flex>
                <Flex direction="column" flex="1">
                    <Text mb={1}>Volume Dir:</Text>
                    <TextForm placeholder="Volume Dir" confirmTitle="Change Volume Dir" value={vol.volumedir} onSave={(value) => onChange(fieldName, index, "volumedir", value)} />
                </Flex>
            </Flex>
        </Flex>
    )
})

const VolumeNewForm = React.memo(({ saveCaption = "Save Volume", onSave = () => { }, poolOptions = [], onCancel = () => { } }) => {
    const [vol, setVol] = useState(defaultVol);

    const valid = useMemo(() => {
        return vol.name && vol.poolname && vol.volumedir;
    }, [vol.name, vol.poolname, vol.volumedir]);

    const handleArrayChange = (key, value) => {
        setVol((prev) => ({ ...prev, [key]: value }));
    }

    const handleSaveAdd = () => {
        if (valid) {
            onSave(vol)
        }
    };

    const handleCancel = () => {
        setVol(defaultVol);
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
