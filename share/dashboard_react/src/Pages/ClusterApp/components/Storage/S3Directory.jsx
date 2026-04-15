import React, { useMemo, useState } from "react";
import { VStack, Input, HStack, Heading, Flex, Box, Text } from "@chakra-ui/react";
import styles from "./styles.module.scss";
import TextForm from "../../../../components/TextForm";
import { createColumnHelper } from "@tanstack/react-table";
import RMButton from "../../../../components/RMButton";
import RMIconButton from "../../../../components/RMIconButton";
import { DataTable } from "../../../../components/DataTable";
import { HiTrash } from "react-icons/hi";
import Dropdown from "../../../../components/Dropdown";

const defaultS3 = { name: "", endpoint: "", bucket: "", region: "", providerName: "" };
const providerSourceOptions = [
  { value: "app", name: "Sibling App" },
  { value: "custom", name: "Custom Endpoint" },
];

const endpointExistsInProviders = (endpoint, s3ProvOptions = []) =>
  !!endpoint && s3ProvOptions.some((opt) => opt.value === endpoint || opt.endpoint === endpoint);

const columnHelper = createColumnHelper()

const S3DirectorySection = ({
    rows = [],
    fieldName = "s3Mounts",
    s3ProvOptions = [],
    clusterS3Providers = [],
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
                        return (<S3DirectoryRowForm fieldName={fieldName} s3ProvOptions={s3ProvOptions} clusterS3Providers={clusterS3Providers} s3directory={row.original} index={row.index} onChange={onRowArrayChange} />);
                    },
                },
                cell: () => null,
            }
        ],
        [fieldName, onRowArrayChange, onRowDropIndex, s3ProvOptions, clusterS3Providers]
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
                        <S3DirectoryNewForm s3ProvOptions={s3ProvOptions} clusterS3Providers={clusterS3Providers} onSave={handleSaveAdd} onCancel={handleCancel} />
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

const S3DirectoryRowForm = React.memo(({ fieldName, s3ProvOptions, clusterS3Providers = [], s3directory, index, onChange }) => {
    const s3 = s3directory || defaultS3;
    const [providerSource, setProviderSource] = useState(() =>
      endpointExistsInProviders(s3.endpoint, s3ProvOptions) ? "app" : "custom"
    );

    const savedProviderOptions = useMemo(() =>
      (clusterS3Providers || [])
        .filter((p) => p.providerSource === "app")
        .map((p) => ({ value: p.name, name: p.name })),
      [clusterS3Providers]
    );

    const onRowArrayChange = (fieldName, index, key, value) => {
        onChange(fieldName, index, key, value);
    };

    const handleProviderSourceChange = (option) => {
      const nextSource = option?.value || option;
      setProviderSource(nextSource);
      if (nextSource === "app" && !endpointExistsInProviders(s3.endpoint, s3ProvOptions) && s3ProvOptions?.length > 0) {
        onRowArrayChange(fieldName, index, "endpoint", s3ProvOptions[0].value);
      }
    };

    const handleSavedProviderChange = (option) => {
      const name = option?.value || option;
      if (!name) return;
      const provider = (clusterS3Providers || []).find((p) => p.name === name);
      if (!provider) return;
      const endpoint = provider.endpoint || provider.providerApp || "";
      onRowArrayChange(fieldName, index, "endpoint", endpoint);
      if (provider.region) {
        onRowArrayChange(fieldName, index, "region", provider.region);
      }
      onRowArrayChange(fieldName, index, "providername", name);
    };

    return (
        <Flex className={styles.variableRowForm} w="100%" align="flex-start" gap={4}>
            <Flex direction="column" flex="1" minW="300px" gap={2}>
                {savedProviderOptions.length > 0 && (
                  <Flex direction="column" flex="1">
                    <Text mb={1}>Saved Provider (optional):</Text>
                    <Dropdown
                      placeholder="Select saved provider to copy settings"
                      selectedValue={s3.providerName || ""}
                      onChange={(option) => handleSavedProviderChange(option)}
                      options={savedProviderOptions}
                    />
                  </Flex>
                )}
                <Flex direction="column" flex="1">
                    <Text mb={1}>Provider Source:</Text>
                    <Dropdown
                      placeholder="Select Provider Source"
                      selectedValue={providerSource}
                      onChange={(option) => handleProviderSourceChange(option)}
                      options={providerSourceOptions}
                    />
                </Flex>
                <Flex direction="column" flex="1">
                    <Text mb={1}>{providerSource === "app" ? "S3 Provider App:" : "Endpoint:"}</Text>
                    {providerSource === "app" ? (
                      <Dropdown
                        confirmTitle={"Confirm provider app change"}
                        placeholder="S3 Provider App"
                        selectedValue={s3.endpoint}
                        onChange={(value) => onRowArrayChange(fieldName, index, "endpoint", value)}
                        options={s3ProvOptions}
                      />
                    ) : (
                      <TextForm
                        placeholder="host:port or https://endpoint"
                        value={s3.endpoint}
                        onSave={(value) => onRowArrayChange(fieldName, index, "endpoint", value)}
                      />
                    )}
                    <Text mt={1} fontSize="sm" color="gray.500">
                      {providerSource === "app"
                        ? "Choose a sibling app configured as an S3 provider."
                        : "Define a custom endpoint (must be reachable and properly configured)."}
                    </Text>
                </Flex>
                <Flex direction="column" flex="1">
                    <Text mb={1}>Bucket:</Text>
                    <TextForm placeholder="Bucket" value={s3.bucket} onSave={(value) => onRowArrayChange(fieldName, index, "bucket", value)} />
                </Flex>
            </Flex>
        </Flex>
    )
})

const S3DirectoryNewForm = React.memo(({ s3ProvOptions = [], clusterS3Providers = [], onSave = () => { }, onCancel = () => { } }) => {
    const [s3, setS3] = useState(defaultS3);
    const [providerSource, setProviderSource] = useState(s3ProvOptions?.length ? "app" : "custom");

    const valid = useMemo(() => {
        return s3.endpoint && s3.bucket;
    }, [s3]);

    const savedProviderOptions = useMemo(() =>
      (clusterS3Providers || [])
        .filter((p) => p.providerSource === "app")
        .map((p) => ({ value: p.name, name: p.name })),
      [clusterS3Providers]
    );

    const handleArrayChange = (key, value) => {
        setS3((prev) => ({ ...prev, [key]: value }));
    }

    const handleProviderSourceChange = (option) => {
      const nextSource = option?.value || option;
      setProviderSource(nextSource);
      if (nextSource === "app") {
        handleArrayChange("endpoint", s3ProvOptions?.[0]?.value || "");
      }
    }

    const handleSavedProviderChange = (option) => {
      const name = option?.value || option;
      if (!name) return;
      const provider = (clusterS3Providers || []).find((p) => p.name === name);
      if (!provider) return;
      const endpoint = provider.endpoint || provider.providerApp || "";
      setS3((prev) => ({
        ...prev,
        endpoint,
        region: provider.region || prev.region,
        accesskey: "",
        secretkey: "",
        providerName: name,
      }));
    };

    const handleSaveAdd = () => {
        if (valid) {
            onSave(s3)
        }
    };

    const handleCancel = () => {
        setS3(defaultS3); // Reset form on cancel
        setProviderSource(s3ProvOptions?.length ? "app" : "custom");
        onCancel();
    };

    return (
        <Flex className={styles.S3DirectoryRowForm} w="100%" align="flex-start" gap={4}>
            <Flex direction="column" flex="1" minW="300px" gap={2}>
                {savedProviderOptions.length > 0 && (
                  <Flex direction="column" flex="1">
                    <Text mb={1}>Saved Provider (optional):</Text>
                    <Dropdown
                      placeholder="Select saved provider to copy settings"
                      selectedValue={s3.providerName || ""}
                      onChange={(option) => handleSavedProviderChange(option)}
                      options={savedProviderOptions}
                    />
                  </Flex>
                )}
                <Flex direction="column" flex="1">
                    <Text mb={1}>Provider Source:</Text>
                    <Dropdown placeholder="Select Provider Source" selectedValue={providerSource} onChange={(option) => handleProviderSourceChange(option)} options={providerSourceOptions} />
                </Flex>
                <Flex direction="column" flex="1">
                    <Text mb={1}>{providerSource === "app" ? "S3 Provider App:" : "Endpoint:"}</Text>
                    {providerSource === "app" ? (
                      <Dropdown placeholder="S3 Provider App" selectedValue={s3.endpoint} onChange={(option) => handleArrayChange("endpoint", option.value)} options={s3ProvOptions} />
                    ) : (
                      <Input placeholder="host:port or https://endpoint" value={s3.endpoint} onChange={(e) => handleArrayChange("endpoint", e.target.value)} />
                    )}
                    <Text mt={1} fontSize="sm" color="gray.500">
                      {providerSource === "app"
                        ? "Choose a sibling app configured as an S3 provider."
                        : "Define a custom endpoint (must be reachable and properly configured)."}
                    </Text>
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
