import React, { useCallback, useEffect, useMemo, useState } from "react";
import PropTypes from "prop-types";
import { VStack, Input, HStack, Heading, Flex, Box, Text } from "@chakra-ui/react";
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

const columnHelper = createColumnHelper()

const formatGitError = (err) => {
    const msg = typeof err === 'string' ? err : (err?.errorMessage || err?.message || '');
    if (/authentication denied/i.test(msg)) {
        return 'Authentication failed: check your username and password/token.';
    }
    if (/not found/i.test(msg) && /branch/i.test(msg)) {
        return msg;
    }
    if (/repository not reachable/i.test(msg) || /timed out/i.test(msg)) {
        return msg;
    }
    return msg || 'Connection check failed.';
};

const maskString = (str, mask = '*') => {
    return str.replaceAll(/./g, mask)
}

// volumedir lists the volume's available top-level directories as a
// whitespace-separated string (see config.Volume.GetVolumeDirs()).
const getVolumeDirTokens = (volumedir) =>
    typeof volumedir === "string" ? volumedir.split(/\s+/).filter(Boolean) : [];

// Mirrors config.Volume.DefaultSubdir(): the first directory token, used as
// the default base path when assigning a volume to a git clone.
const defaultSubdir = (volumedir) => getVolumeDirTokens(volumedir)[0] || "";

// Finds which of the volume's directory tokens `path` is rooted under
// (either equal to the token or "<token>/..."), falling back to the
// volume's default subdir if no token matches (e.g. legacy paths predating
// the multi-directory volume merge).
const matchVolumeDirToken = (path, volumedir) => {
    const dirs = getVolumeDirTokens(volumedir);
    const match = dirs.find((dir) => path === dir || path.startsWith(`${dir}/`));
    return match || defaultSubdir(volumedir);
};

// Splits a persisted GitClone.volumedir into the portion beneath
// `baseDirToken`, the inverse of buildVolumeDir. Falls back to the full value
// when it isn't rooted under baseDirToken (e.g. a legacy path predating the
// multi-directory volume merge, or a volume with no directory tokens at all).
const extractSubDir = (volumedir, baseDirToken) => {
    if (!baseDirToken) return volumedir;
    if (volumedir === baseDirToken) return "";
    if (volumedir.startsWith(`${baseDirToken}/`)) return volumedir.substring(baseDirToken.length + 1);
    return volumedir;
};

// Builds the full GitClone.volumedir to persist from a selected base
// directory token and a subdirectory beneath it, defaulting a blank
// subdirectory to `cloneNameFallback` (the git clone name).
const buildVolumeDir = (baseDirToken, subDir, cloneNameFallback) => {
    const trimmed = typeof subDir === "string" ? subDir.trim() : "";
    const sub = !trimmed || trimmed === "/" ? cloneNameFallback : subDir;
    const cleanSub = sub.startsWith("/") ? sub.slice(1) : sub;
    return baseDirToken ? `${baseDirToken}/${cleanSub}` : cleanSub;
};

const GitCloneSection = React.memo(function GitCloneSection({
    rows = [],
    volumeOptions = [],
    fieldName = "gitClones",
    onRowArrayChange,
    onRowDropIndex,
    onSaveAdd,
    onCheckGit = null,
    onCheckGitNew = null,
    onPauseAutoReload = () => { },
    onResumeAutoReload = () => { },
}) {
    const [isVisible, setIsVisible] = useState(false);

    const handleAddItem = () => {
        setIsVisible(true);
        onPauseAutoReload();
    };

    const handleCancel = () => {
        setIsVisible(false);
        onResumeAutoReload();
    };

    const handleSaveAdd = useCallback((formData) => {
        onSaveAdd(fieldName, formData).then(() => {
            setIsVisible(false);
            onResumeAutoReload();
        }).catch(() => {
            // Error banner shown by redux. Resume auto-reload so the UI doesn't stay paused.
            onResumeAutoReload();
        });
    }, [fieldName, onSaveAdd, onResumeAutoReload]);

    const columnsRowForm = useMemo(
        () => [
            columnHelper.accessor((row) => row.name, {
                header: 'Name'
            }),
            columnHelper.accessor((row) => row.repo, {
                header: 'Repo',
                cell: (info) => info.getValue() || "N/A",
            }),
            columnHelper.accessor((row) => row.branch, {
                header: 'Branch',
                cell: (info) => info.getValue() || "N/A",
            }),
            columnHelper.accessor((row) => row.user, {
                header: 'User',
                cell: (info) => info.getValue() || "N/A",
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
                    renderExpansion: (row) => (
                        <GitRowForm
                            key={row.index}
                            fieldName={fieldName}
                            volumeOptions={volumeOptions}
                            gitClone={row.original}
                            index={row.index}
                            onChange={onRowArrayChange}
                            onCheckGit={onCheckGit}
                        />
                    ),
                },
                cell: () => null,
            }
        ],
        [fieldName, onRowArrayChange, onRowDropIndex, volumeOptions, onCheckGit]
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
                        <GitNewForm volumeOptions={volumeOptions} onSave={handleSaveAdd} onCancel={handleCancel} onCheckGit={onCheckGitNew} />
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

const GitRowForm = React.memo(function GitRowForm({ fieldName, gitClone, index, onChange, volumeOptions, onCheckGit = null }) {
    const gc = gitClone || defaultGit;
    const { theme } = useTheme();
    const { name, repo, branch, user, pass, volumename, volumedir } = gc;
    const [checkState, setCheckState] = useState({ status: 'idle', message: '' });

    useEffect(() => {
        setCheckState({ status: 'idle', message: '' });
    }, [gc.name]);

    const vol = useMemo(() => volumeOptions.find((opt) => opt.value === volumename), [volumeOptions, volumename]);
    const availableDirs = useMemo(() => getVolumeDirTokens(vol?.volumedir), [vol]);
    const srcbasepath = useMemo(() => matchVolumeDirToken(volumedir, vol?.volumedir), [volumedir, vol]);
    const subpath = useMemo(() => extractSubDir(volumedir, srcbasepath), [volumedir, srcbasepath]);

    const handleVolume = useCallback((value) => {
        const newVol = volumeOptions.find((opt) => opt.value === value);
        const newDirs = getVolumeDirTokens(newVol?.volumedir);
        const newBase = newDirs.includes(srcbasepath) ? srcbasepath : defaultSubdir(newVol?.volumedir);
        const newValue = newVol ? buildVolumeDir(newBase, subpath, name) : volumedir;
        onChange(fieldName, index, "volumename", newVol ? newVol.value : "");
        onChange(fieldName, index, "volumedir", newValue);
    }, [fieldName, index, volumeOptions, srcbasepath, subpath, name, volumedir, onChange]);

    const handleBaseDir = useCallback((value) => {
        onChange(fieldName, index, "volumedir", buildVolumeDir(value, subpath, name));
    }, [fieldName, index, subpath, name, onChange]);

    const handleSubPath = useCallback((value) => {
        onChange(fieldName, index, "volumedir", buildVolumeDir(srcbasepath, value, name));
    }, [fieldName, index, srcbasepath, name, onChange]);

    const handleCheckConnection = useCallback(async () => {
        if (!onCheckGit || !gc.name) return;
        setCheckState({ status: 'checking', message: '' });
        try {
            await onCheckGit(gc.name);
            setCheckState({ status: 'ok', message: 'Connection successful.' });
        } catch (err) {
            setCheckState({ status: 'error', message: formatGitError(err) });
        }
    }, [onCheckGit, gc.name]);

    return (
        <Flex className={styles.variableRowForm} w="100%" align="flex-start" gap={4}>
            <Flex direction="column" flex="1" minW="300px" gap={2}>
                <Flex direction="column" flex="1">
                    <Text mb={1}>Name:</Text>
                    <TextForm placeholder="Name" value={name} onSave={(value) => onChange(fieldName, index, "name", value)} />
                </Flex>
                <Flex direction="column" flex="1">
                    <Text mb={1}>Repo:</Text>
                    <TextForm placeholder="Repo" value={repo} onSave={(value) => onChange(fieldName, index, "repo", value)} />
                </Flex>
                <Flex direction="column" flex="1">
                    <Text mb={1}>Branch:</Text>
                    <TextForm placeholder="Branch" value={branch} onSave={(value) => onChange(fieldName, index, "branch", value)} />
                </Flex>
                <Flex direction="column" flex="1">
                    <Text mb={1}>User:</Text>
                    <TextForm placeholder="User" value={user} onSave={(value) => onChange(fieldName, index, "user", value)} />
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
                        onSave={(value) => onChange(fieldName, index, "pass", value)} />
                </Flex>
                {onCheckGit && (
                    <Flex direction="column" flex="1" gap={1}>
                        <RMButton
                            onClick={handleCheckConnection}
                            isDisabled={checkState.status === 'checking' || !gc.name}
                            size="sm"
                        >
                            {checkState.status === 'checking' ? 'Checking…' : 'Check Connection'}
                        </RMButton>
                        {checkState.status === 'ok' && (
                            <Text fontSize="sm" color="green.400">{checkState.message}</Text>
                        )}
                        {checkState.status === 'error' && (
                            <Text fontSize="sm" color="red.400">{checkState.message}</Text>
                        )}
                    </Flex>
                )}
                <Flex direction="column" flex="1">
                    <Text mb={1}>Volume:</Text>
                    <Dropdown key={`volume-${index}`} placeholder="Volume" confirmTitle="Change Volume" options={volumeOptions} selectedValue={volumename} onChange={(value) => handleVolume(value)} />
                </Flex>
                {availableDirs.length > 1 ? (
                    <Flex direction="column" flex="1">
                        <Text mb={1}>Volume Dir:</Text>
                        <Dropdown
                            key={`git-basedir-${index}`}
                            placeholder="Volume Dir"
                            confirmTitle="Change Volume Dir"
                            options={availableDirs.map((dir) => ({ value: dir, name: dir }))}
                            selectedValue={srcbasepath}
                            onChange={(value) => handleBaseDir(value)}
                        />
                    </Flex>
                ) : (
                    srcbasepath && (<Text key={`${volumename}-basepath`} mb={1} fontSize="sm" color="gray.500">Basepath: {srcbasepath}</Text>)
                )}
                <Flex direction="column" flex="1">
                    <Text mb={1}>Sub Dir:</Text>
                    <TextForm key={`volume-dir-${index}`} placeholder="Volume Sub Dir" confirmTitle="Change Volume Sub Dir" value={subpath} onSave={(value) => handleSubPath(value)} />
                    <Text>Fullpath: {volumedir}</Text>
                </Flex>
            </Flex>
        </Flex>
    )
})

const GitNewForm = React.memo(function GitNewForm({ volumeOptions, onSave = () => { }, onCancel = () => { }, onCheckGit = null }) {
    const [gc, setGc] = useState(defaultGit);
    const [checkState, setCheckState] = useState({ status: 'idle', message: '' });
    const { theme } = useTheme();
    const { name, repo, branch, user, pass, volumename, volumedir, subpath } = gc;

    const vol = useMemo(() => volumeOptions.find((opt) => opt.value === volumename), [volumeOptions, volumename]);
    const availableDirs = useMemo(() => getVolumeDirTokens(vol?.volumedir), [vol]);
    const srcbasepath = useMemo(() => matchVolumeDirToken(volumedir, vol?.volumedir), [volumedir, vol]);

    const valid = useMemo(() => name && repo && branch && volumedir, [name, repo, branch, volumedir]);

    const handleArrayChange = useCallback((key, value) => {
        setGc((prev) => ({ ...prev, [key]: value }));
    }, []);

    const handleSaveAdd = useCallback(() => {
        if (valid) onSave(gc);
    }, [onSave, gc, valid]);

    const handleCancel = useCallback(() => {
        setGc(defaultGit);
        onCancel();
    }, [onCancel]);

    const handleVolume = useCallback((option) => {
        const newDirs = getVolumeDirTokens(option?.volumedir);
        const newBase = newDirs.includes(srcbasepath) ? srcbasepath : defaultSubdir(option?.volumedir);
        handleArrayChange("volumename", option.value);
        handleArrayChange("volumedir", buildVolumeDir(newBase, subpath, name));
    }, [name, subpath, srcbasepath, handleArrayChange]);

    const handleBaseDir = useCallback((option) => {
        handleArrayChange("volumedir", buildVolumeDir(option.value, subpath, name));
    }, [subpath, name, handleArrayChange]);

    const handleSubPath = useCallback((value) => {
        handleArrayChange("subpath", value);
        handleArrayChange("volumedir", buildVolumeDir(srcbasepath, value, name));
    }, [name, srcbasepath, handleArrayChange]);

    const handleCheckRepo = useCallback(async () => {
        if (!onCheckGit || !repo || !branch) return;
        setCheckState({ status: 'checking', message: '' });
        try {
            const result = await onCheckGit({ repo, branch, user, pass });
            setCheckState({
                status: result?.data?.ok ? 'ok' : 'error',
                message: result?.data?.message || (result?.data?.ok ? 'Repository reachable.' : 'Check failed.'),
            });
        } catch (err) {
            setCheckState({ status: 'error', message: formatGitError(err) });
        }
    }, [onCheckGit, repo, branch, user, pass]);

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
                {onCheckGit && (
                    <Flex direction="column" flex="1" gap={1}>
                        <RMButton
                            onClick={handleCheckRepo}
                            isDisabled={checkState.status === 'checking' || !repo || !branch}
                            size="sm"
                        >
                            {checkState.status === 'checking' ? 'Checking…' : 'Check Repo'}
                        </RMButton>
                        {checkState.status === 'ok' && (
                            <Text fontSize="sm" color="green.400">{checkState.message}</Text>
                        )}
                        {checkState.status === 'error' && (
                            <Text fontSize="sm" color="red.400">{checkState.message}</Text>
                        )}
                    </Flex>
                )}
                <Flex direction="column" flex="1">
                    <Text mb={1}>Volume:</Text>
                    <Dropdown key={`volume-new`} placeholder="Volume" options={volumeOptions} selectedValue={volumename} onChange={(option) => handleVolume(option)} />
                </Flex>
                {availableDirs.length > 1 ? (
                    <Flex direction="column" flex="1">
                        <Text mb={1}>Volume Dir:</Text>
                        <Dropdown
                            key={`git-basedir-new`}
                            placeholder="Volume Dir"
                            options={availableDirs.map((dir) => ({ value: dir, name: dir }))}
                            selectedValue={srcbasepath}
                            onChange={(option) => handleBaseDir(option)}
                        />
                    </Flex>
                ) : (
                    srcbasepath && (<Text key={`${volumename}-basepath`} mb={1} fontSize="sm" color="gray.500">Basepath: {srcbasepath}</Text>)
                )}
                <Flex direction="column" flex="1">
                    <Text mb={1}>Sub Dir:</Text>
                    <Input key={`volume-dir-new`} placeholder="Volume Sub Dir" value={subpath} onChange={(e) => handleSubPath(e.target.value)} />
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

const volumeOptionShape = PropTypes.shape({
    value: PropTypes.string,
    name: PropTypes.string,
    volumedir: PropTypes.string,
})

GitCloneSection.propTypes = {
    rows: PropTypes.array,
    volumeOptions: PropTypes.arrayOf(volumeOptionShape),
    fieldName: PropTypes.string,
    onRowArrayChange: PropTypes.func,
    onRowDropIndex: PropTypes.func,
    onSaveAdd: PropTypes.func,
    onCheckGit: PropTypes.func,
    onCheckGitNew: PropTypes.func,
    onPauseAutoReload: PropTypes.func,
    onResumeAutoReload: PropTypes.func,
}

GitRowForm.propTypes = {
    fieldName: PropTypes.string,
    gitClone: PropTypes.object,
    index: PropTypes.number,
    onChange: PropTypes.func,
    volumeOptions: PropTypes.arrayOf(volumeOptionShape),
    onCheckGit: PropTypes.func,
}

GitNewForm.propTypes = {
    volumeOptions: PropTypes.arrayOf(volumeOptionShape),
    onSave: PropTypes.func,
    onCancel: PropTypes.func,
    onCheckGit: PropTypes.func,
}

export default GitCloneSection
