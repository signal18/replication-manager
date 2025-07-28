import React, { useMemo, useState } from "react";
import { VStack, Input, HStack, Heading, Flex, Select, Box, Text } from "@chakra-ui/react";
import styles from "./styles.module.scss";
import TextForm from "../../../../components/TextForm";
import { createColumnHelper } from "@tanstack/react-table";
import RMButton from "../../../../components/RMButton";
import RMIconButton from "../../../../components/RMIconButton";
import { DataTable } from "../../../../components/DataTable";
import { HiTrash } from "react-icons/hi";
import Dropdown from "../../../../components/Dropdown";

const defaultS3 = { name: "", endpoint:"", bucket: "" };

const columnHelper = createColumnHelper()

const S3DirectorySection = ({
    rows = [],
    fieldName = "s3Directories",
    s3ProvOptions = [],
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
            columnHelper.accessor((row) => row.endpoint, {
                header: 'Endpoint'
            }),
            columnHelper.accessor((row) => row.bucket, {
                header: 'Bucket',
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
                        return (<S3DirectoryRowForm fieldName={fieldName} s3ProvOptions={s3ProvOptions} s3directory={row.original} index={row.index} onChange={onRowArrayChange} />);
                    },
                },
                cell: () => null,
            }
        ],
        [fieldName, onRowArrayChange, onRowDropIndex, s3ProvOptions]
    )

    return (
        <Flex direction="column" className={`${styles.sectionWrapper}`}>
            <VStack spacing={3} align="stretch">
                <Heading as="h3" size="md">
                    Saved S3 Directories
                </Heading>
                <Box className={styles.tableContainer}>
                    <DataTable key="app-variables" data={rows} columns={columnsRowForm} className={styles.table} enableExpanding={true} enableExpandingNoSubRows={true} />
                </Box>
            </VStack>
            {isVisible ? (
                <VStack spacing={3} align="stretch">
                    <Heading as="h3" size="md">
                        Add New S3 Directory
                    </Heading>
                    <Box className={styles.tableContainer}>
                        <S3DirectoryNewForm s3ProvOptions={s3ProvOptions} onSave={handleSaveAdd} onCancel={handleCancel} />
                    </Box>
                </VStack>
            ) : (
                <VStack spacing={3} align="stretch">
                    <HStack>
                        <RMButton onClick={handleAddItem}>
                            Add S3 Directory
                        </RMButton>
                    </HStack>
                </VStack>
            )}
        </Flex>
    )
}

export default React.memo(S3DirectorySection);

const S3DirectoryRowForm = React.memo(({ fieldName, s3ProvOptions, s3directory, index, onChange }) => {
    const s3 = s3directory || defaultS3;

    const onRowArrayChange = (fieldName, index, key, value) => {
        onChange(fieldName, index, key, value);
    };

    return (
        <Flex className={styles.variableRowForm} w="100%" align="flex-start" gap={4}>
            <Flex direction="column" flex="1" minW="300px" gap={2}>
                <Flex direction="column" flex="1">
                    <Text mb={1}>Endpoint:</Text>
                    <Dropdown confirmTitle={"Confirm endpoint change"} placeholder="Endpoint" selectedValue={s3.endpoint} onChange={(value) => onRowArrayChange(fieldName, index, "endpoint", value)} options={s3ProvOptions} />
                </Flex>
                <Flex direction="column" flex="1">
                    <Text mb={1}>Bucket:</Text>
                    <TextForm placeholder="Bucket" value={s3.bucket} onSave={(value) => onRowArrayChange(fieldName, index, "bucket", value)} />
                </Flex>
            </Flex>
        </Flex>
    )
})

const S3DirectoryNewForm = React.memo(({ s3ProvOptions = [], onSave = () => { }, onCancel = () => { } }) => {
    const [s3, setS3] = useState(defaultS3);

    const valid = useMemo(() => {
        return s3.endpoint && s3.bucket;
    }, [s3]);

    const handleArrayChange = (key, value) => {
        setS3((prev) => ({ ...prev, [key]: value }));
    }

    const handleSaveAdd = () => {
        if (valid) {
            onSave(s3)
        }
    };

    const handleCancel = () => {
        setS3(defaultS3); // Reset form on cancel
        onCancel();
    };

    return (
        <Flex className={styles.S3DirectoryRowForm} w="100%" align="flex-start" gap={4}>
            <Flex direction="column" flex="1" minW="300px" gap={2}>
                <Flex direction="column" flex="1">
                    <Text mb={1}>Endpoint:</Text>
                    <Dropdown placeholder="Endpoint" selectedValue={s3.endpoint} onChange={(option) => handleArrayChange("endpoint", option.value)} options={s3ProvOptions} />
                </Flex>
                <Flex direction="column" flex="1">
                    <Text mb={1}>Bucket:</Text>
                    <Input placeholder="Bucket Name" value={s3.bucket} onChange={(e) => handleArrayChange("bucket", e.target.value)} />
                </Flex>
                <Flex direction="column" flex="1">
                    <HStack spacing={2} mt={4}>
                        <RMButton onClick={handleCancel}>
                            Clear Form
                        </RMButton>
                        <RMButton onClick={handleSaveAdd} isDisabled={!valid}>
                            Save S3 Directory
                        </RMButton>
                    </HStack>
                </Flex>
            </Flex>
        </Flex>
    )
})