import React, { useCallback, useMemo, useState } from "react";
import { VStack, Input, HStack, Heading, Flex, Select, Box, Text } from "@chakra-ui/react";
import styles from "./styles.module.scss";
import PasswordControl from "../../../../components/PasswordControl";
import TextForm from "../../../../components/TextForm";
import { createColumnHelper } from "@tanstack/react-table";
import RMButton from "../../../../components/RMButton";
import RMIconButton from "../../../../components/RMIconButton";
import Dropdown from "../../../../components/Dropdown";
import { DataTable } from "../../../../components/DataTable";
import { HiTrash } from "react-icons/hi";
import { useTheme } from "../../../../ThemeProvider";

const defaultGit = {
    name: "",
    branch: "",
    pass: "",
    repo: "",
    timeout: 0,
    user: "",
    volumename: "",
    volumedir: "",
    subpath: "",
};

const volumeDirs = ["etc", "var", "log"].map((dir) => ({ value: dir, name: dir }));

const columnHelper = createColumnHelper()

const maskString = (str, mask = '*') => {
    return str.replaceAll(/./g, mask)
}

export default React.memo(function GitCloneSection({
    rows = [],
    volumeOptions = [],
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

    const handleSaveAdd = useCallback((formData) => {
      onSaveAdd(fieldName, formData).then(() => {
        setIsVisible(false); // Hide the form after saving
        onResumeAutoReload(); // Resume auto-reload after saving
        return Promise.resolve();
      }, (error) => {
        return Promise.reject(error);
      });
  },[fieldName, onSaveAdd, onResumeAutoReload]);

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
            columnHelper.accessor((row) => row.volumename, {
                header: 'Volume',
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
                        return (<GitRowForm key={row.index} fieldName={fieldName} volumeOptions={volumeOptions} gitClone={row.original} index={row.index} onChange={onRowArrayChange} />);
                    },
                },
                cell: () => null,
            }
        ],
        [fieldName, onRowArrayChange, onRowDropIndex, volumeOptions]
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
                        <GitNewForm volumeOptions={volumeOptions} onSave={handleSaveAdd} onCancel={handleCancel} />
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

const GitRowForm = React.memo(({ fieldName, gitClone, index, onChange, volumeOptions }) => {
    const gc = gitClone || defaultGit;
    const { theme } = useTheme();
    const { name, repo, branch, user, pass, volumename, volumedir } = gc;

    const vol = useMemo(() => volumeOptions.find((opt) => opt.value === volumename), [volumeOptions, volumename]);
    const srcbasepath = vol ? vol.volumedir : "";
    // Substract srcbasepath
    const subpath = useMemo(() => volumedir.startsWith(srcbasepath) ? volumedir.substring(srcbasepath.length + 1) : volumedir, [volumedir, srcbasepath]);

    const onRowArrayChange = useCallback((fieldName, index, key, value) => {
        onChange(fieldName, index, key, value);
    }, [onChange]);

    const handleVolume = useCallback((value) => {
        const vol = volumeOptions.find((opt) => opt.value === value);
        const newValue = vol ? vol.volumedir + (subpath.startsWith("/") ? subpath : "/" + subpath) : volumedir;
        onRowArrayChange(fieldName, index, "volumename", vol ? vol.value : "");
        onRowArrayChange(fieldName, index, "volumedir", newValue);
    }, [fieldName, index, volumeOptions, subpath, onRowArrayChange]);

    const handleSubPath = useCallback((value) => {
        value = !value.trim() || value.trim() === "/" ? name : value;
        const newValue = srcbasepath + (value.startsWith("/") ? value : "/" + value);
        onRowArrayChange(fieldName, index, "volumedir", newValue);
    }, [fieldName, index, name, srcbasepath, onRowArrayChange]);

    return (
        <Flex className={styles.variableRowForm} w="100%" align="flex-start" gap={4}>
            <Flex direction="column" flex="1" minW="300px" gap={2}>
                <Flex direction="column" flex="1">
                    <Text mb={1}>Name:</Text>
                    <TextForm placeholder="Name" value={name} onSave={(value) => onRowArrayChange(fieldName, index, "name", value)} />
                </Flex>
                <Flex direction="column" flex="1">
                    <Text mb={1}>Repo:</Text>
                    <TextForm placeholder="Repo" value={repo} onSave={(value) => onRowArrayChange(fieldName, index, "repo", value)} />
                </Flex>
                <Flex direction="column" flex="1">
                    <Text mb={1}>Branch:</Text>
                    <TextForm placeholder="Branch" value={branch} onSave={(value) => onRowArrayChange(fieldName, index, "branch", value)} />
                </Flex>
                <Flex direction="column" flex="1">
                    <Text mb={1}>User:</Text>
                    <TextForm placeholder="User" value={user} onSave={(value) => onRowArrayChange(fieldName, index, "user", value)} />
                </Flex>
                <Flex direction="column" flex="1">
                    <Text mb={1}>Password:</Text>
                    <TextForm
                        type="password"
                        noControl={true}
                        inputClassName={theme === 'dark' ? styles.darkLoginText : ""}
                        labelClassName={theme === 'dark' ? styles.darkLoginText : ""}
                        placeholder="Password"
                        value={pass}
                        onSave={(value) => onRowArrayChange(fieldName, index, "pass", value)} />
                </Flex>
                <Flex direction="column" flex="1">
                    <Text mb={1}>Volume:</Text>
                    <Dropdown key={`volume-${index}`} placeholder="Volume" confirmTitle="Change Volume" options={volumeOptions} selectedValue={volumename} onChange={(value) => handleVolume(value)} />
                    {srcbasepath && (<Text key={volumename} mb={1} fontSize="sm" color="gray.500">Basepath: {srcbasepath}</Text>)}
                </Flex>
                <Flex direction="column" flex="1">
                    <Text mb={1}>Sub Dir:</Text>
                    <TextForm key={`volume-dir-${index}`} placeholder="Volume Sub Dir" confirmTitle="Change Volume Sub Dir" value={subpath} onSave={(value) => handleSubPath(value)} />
                    <Text>Fullpath: {volumedir}</Text>
                </Flex>
            </Flex>
        </Flex>
    )
})

const GitNewForm = React.memo(({ volumeOptions, onSave = () => { }, onCancel = () => { } }) => {
    const [gc, setGc] = useState(defaultGit);
    const { theme } = useTheme();
    const { name, repo, branch, user, pass, volumename, volumedir, subpath } = gc;

    const srcbasepath = useMemo(() => {
        const vol = volumeOptions.find((opt) => opt.value === volumename);
        return vol ? vol.volumedir : "";
    }, [volumeOptions, volumename]);

    const valid = useMemo(() => {
        return name && repo && branch && volumedir;
    }, [name, repo, branch, volumedir]);

    const handleArrayChange = useCallback((key, value) => {
        setGc((prev) => ({ ...prev, [key]: value }));
    },[]);

    const handleSaveAdd = useCallback(() => {
        if (valid) {
            onSave(gc);
        }
    }, [onSave, gc, valid]);

    const handleCancel = useCallback(() => {
        setGc(defaultGit); // Reset form on cancel
        onCancel();
    }, [onCancel, setGc]);

    const handleVolume = useCallback((option) => {
        const newSubpath = !subpath.trim() || subpath.trim() === "/" ? name : subpath;
        const newValue = option.volumedir + (newSubpath.startsWith("/") ? newSubpath : "/" + newSubpath);
        handleArrayChange("volumename", option.value);
        handleArrayChange("volumedir", newValue);
    }, [volumeOptions, name, subpath, handleArrayChange]);

    const handleSubPath = useCallback((value) => {
        handleArrayChange("subpath", value);
        const newSubpath = !value.trim() || value.trim() === "/" ? name : value;
        const newValue = srcbasepath + (newSubpath.startsWith("/") ? newSubpath : "/" + newSubpath);
        handleArrayChange("volumedir", newValue);
    }, [name, srcbasepath, handleArrayChange]);

    return (
        <Flex className={styles.gitRowForm} w="100%" align="flex-start" gap={4}>
            <Flex direction="column" flex="1" minW="300px" gap={2}>
                <Flex direction="column" flex="1">
                    <Text mb={1}>Name:</Text>
                    <Input placeholder="Name" value={name} onChange={(e) => handleArrayChange("name", e.target.value)} />
                </Flex>
                <Flex direction="column" flex="1">
                    <Text mb={1}>Repo:</Text>
                    <Input placeholder="Repo" value={repo} onChange={(e) => handleArrayChange("repo", e.target.value)} />
                </Flex>
                <Flex direction="column" flex="1">
                    <Text mb={1}>Branch:</Text>
                    <Input placeholder="Branch" value={branch} onChange={(e) => handleArrayChange("branch", e.target.value)} />
                </Flex>
                <Flex direction="column" flex="1">
                    <Text mb={1}>User:</Text>
                    <Input placeholder="User" value={user} onChange={(e) => handleArrayChange("user", e.target.value)} />
                </Flex>
                <Flex direction="column" flex="1">
                    <Text mb={1}>Password:</Text>
                    <PasswordControl
                        noControl={true}
                        inputClassName={theme === 'dark' ? styles.darkLoginText : ""}
                        labelClassName={theme === 'dark' ? styles.darkLoginText : ""}
                        placeholder="Password"
                        value={pass}
                        onChange={(e) => handleArrayChange("pass", e.target.value)} />
                </Flex>
                <Flex direction="column" flex="1">
                    <Text mb={1}>Volume:</Text>
                    <Dropdown key={`volume-new`} placeholder="Volume" options={volumeOptions} selectedValue={volumename} onChange={(option) => handleVolume(option)} />
                    {srcbasepath && (<Text key={volumename} mb={1} fontSize="sm" color="gray.500">Basepath: {srcbasepath}</Text>)}
                </Flex>
                <Flex direction="column" flex="1">
                    <Text mb={1}>Volume Dir:</Text>
                    <Input key={`volume-dir-new`} placeholder="Volume Dir" value={subpath} onChange={(e) => handleSubPath(e.target.value)} />
                    <Text>Fullpath: {volumedir}</Text>
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