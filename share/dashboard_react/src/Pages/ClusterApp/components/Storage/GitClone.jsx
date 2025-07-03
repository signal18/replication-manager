import React, { useMemo, useState } from "react";
import { VStack, Input, HStack, Heading, Flex, Select, Box, useTheme, Text } from "@chakra-ui/react";
import styles from "./styles.module.scss";
import PasswordControl from "../../../../components/PasswordControl";
import TextForm from "../../../../components/TextForm";
import { createColumnHelper } from "@tanstack/react-table";
import RMButton from "../../../../components/RMButton";
import RMIconButton from "../../../../components/RMIconButton";
import Dropdown from "../../../../components/Dropdown";
import { DataTable } from "../../../../components/DataTable";
import { HiTrash } from "react-icons/hi";

const defaultGit = {
    name: "",
    branch: "",
    pass: "",
    repo: "",
    timeout: 0,
    user: "",
    volumedir: "",
};

const volumeDirs = ["etc", "var", "log"].map((dir) => ({ value: dir, name: dir }));

const columnHelper = createColumnHelper()

const maskString = (str, mask = '*') => {
    return str.replaceAll(/./g, mask)
}

export default React.memo(function GitCloneSection({
    rows = [],
    fieldName = "gitClones",
    onRowArrayChange,
    onRowDropIndex,
    onSaveAdd,
    onPauseAutoReload = () => { },
    onResumeAutoReload = () => { },
}) {
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
            columnHelper.accessor((row) => row.repo, {
                header: 'Repo',
                cell: (info) => {
                    const repo = info.getValue();
                    return repo ? repo : "N/A";
                },
            }),
            columnHelper.accessor((row) => row.branch, {
                header: 'Branch',
                cell: (info) => {
                    const branch = info.getValue();
                    return branch ? branch : "N/A";
                },
            }),
            columnHelper.accessor((row) => row.user, {
                header: 'User',
                cell: (info) => {
                    const user = info.getValue();
                    return user ? user : "N/A";
                },
            }),
            columnHelper.accessor((row) => row.pass, {
                header: 'Password',
                cell: (info) => {
                    const pass = info.getValue();
                    return pass ? maskString(pass) : "N/A";
                },
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
                        return (<GitRowForm fieldName={fieldName} gitClone={row.original} index={row.index} onChange={onRowArrayChange} />);
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
                    Saved Git Clones
                </Heading>
                <Box className={styles.tableContainer}>
                    <DataTable key="app-variables" data={rows} columns={columnsRowForm} className={styles.table} enableExpanding={true} enableExpandingNoSubRows={true} />
                </Box>
            </VStack>
            {isVisible ? (
                <VStack spacing={3} align="stretch">
                    <Heading as="h3" size="md">
                        Add New Git Clone
                    </Heading>
                    <Box className={styles.tableContainer}>
                        <GitNewForm onSave={handleSaveAdd} onCancel={handleCancel} />
                    </Box>
                </VStack>
            ) : (
                <VStack spacing={3} align="stretch">
                    <HStack>
                        <RMButton onClick={handleAddItem}>
                            Add Git Clone
                        </RMButton>
                    </HStack>
                </VStack>
            )}
        </Flex>
    )
})

const GitRowForm = React.memo(({ fieldName, gitClone, index, onChange }) => {
    const gc = gitClone || defaultGit;

    const onRowArrayChange = (fieldName, index, key, value) => {
        onChange(fieldName, index, key, value);
    };

    return (
        <Flex className={styles.variableRowForm} w="100%" align="flex-start" gap={4}>
            <Flex direction="column" flex="1" minW="300px" gap={2}>
                <Flex direction="column" flex="1">
                    <Text mb={1}>Name:</Text>
                    <TextForm placeholder="Name" value={gc.name} onSave={(value) => onRowArrayChange(fieldName, index, "name", value)} />
                </Flex>
                <Flex direction="column" flex="1">
                    <Text mb={1}>Repo:</Text>
                    <TextForm placeholder="Repo" value={gc.repo} onSave={(value) => onRowArrayChange(fieldName, index, "repo", value)} />
                </Flex>
                <Flex direction="column" flex="1">
                    <Text mb={1}>Branch:</Text>
                    <TextForm placeholder="Branch" value={gc.branch} onSave={(value) => onRowArrayChange(fieldName, index, "branch", value)} />
                </Flex>
                <Flex direction="column" flex="1">
                    <Text mb={1}>User:</Text>
                    <TextForm placeholder="User" value={gc.user} onSave={(value) => onRowArrayChange(fieldName, index, "user", value)} />
                </Flex>
                <Flex direction="column" flex="1">
                    <Text mb={1}>Password:</Text>
                    <TextForm
                        type="password"
                        noControl={true}
                        inputClassName={theme === 'dark' ? styles.darkLoginText : ""}
                        labelClassName={theme === 'dark' ? styles.darkLoginText : ""}
                        placeholder="Password"
                        value={gc.pass}
                        onSave={(value) => onRowArrayChange(fieldName, index, "pass", value)} />
                </Flex>
                <Flex direction="column" flex="1">
                    <Text mb={1}>Volume Dir:</Text>
                    <Dropdown placeholder="Volume Dir" confirmTitle="Change Volume Dir" options={volumeDirs} selectedValue={gc.volumedir} onChange={(value) => onRowArrayChange(fieldName, index, "volumedir", value)} />
                </Flex>
            </Flex>
        </Flex>
    )
})

const GitNewForm = React.memo(({ onSave = () => { }, onCancel = () => { } }) => {
    const [gc, setGc] = useState(defaultGit);
    const { theme } = useTheme();

    const valid = useMemo(() => {
        return gc.name && gc.repo && gc.branch && gc.volumedir;
    }, [gc]);

    const handleArrayChange = (key, value) => {
        setGc((prev) => ({ ...prev, [key]: value }));
    }

    const handleSaveAdd = () => {
        if (valid) {
            onSave(gc)
        }
    };

    const handleCancel = () => {
        setGc(defaultGit); // Reset form on cancel
        onCancel();
    };

    return (
        <Flex className={styles.gitRowForm} w="100%" align="flex-start" gap={4}>
            <Flex direction="column" flex="1" minW="300px" gap={2}>
                <Flex direction="column" flex="1">
                    <Text mb={1}>Name:</Text>
                    <Input placeholder="Name" value={gc.name} onChange={(e) => handleArrayChange("name", e.target.value)} />
                </Flex>
                <Flex direction="column" flex="1">
                    <Text mb={1}>Repo:</Text>
                    <Input placeholder="Repo" value={gc.repo} onChange={(e) => handleArrayChange("repo", e.target.value)} />
                </Flex>
                <Flex direction="column" flex="1">
                    <Text mb={1}>Branch:</Text>
                    <Input placeholder="Branch" value={gc.branch} onChange={(e) => handleArrayChange("branch", e.target.value)} />
                </Flex>
                <Flex direction="column" flex="1">
                    <Text mb={1}>User:</Text>
                    <Input placeholder="User" value={gc.user} onChange={(e) => handleArrayChange("user", e.target.value)} />
                </Flex>
                <Flex direction="column" flex="1">
                    <Text mb={1}>Password:</Text>
                    <PasswordControl
                        noControl={true}
                        inputClassName={theme === 'dark' ? styles.darkLoginText : ""}
                        labelClassName={theme === 'dark' ? styles.darkLoginText : ""}
                        placeholder="Password"
                        value={gc.pass}
                        onChange={(e) => handleArrayChange("pass", e.target.value)} />
                </Flex>
                <Flex direction="column" flex="1">
                    <Text mb={1}>Volume Dir:</Text>
                    <Dropdown placeholder="Volume Dir" options={volumeDirs} selectedValue={gc.volumedir} onChange={(option) => handleArrayChange("volumedir", option.value)} />
                </Flex>
                <Flex direction="column" flex="1">
                    <HStack spacing={2} mt={4}>
                        <RMButton onClick={handleCancel}>
                            Clear Form
                        </RMButton>
                        <RMButton onClick={handleSaveAdd} isDisabled={!valid}>
                            Save Git Clone
                        </RMButton>
                    </HStack>
                </Flex>
            </Flex>
        </Flex>
    )
})