import { Box, FormControl, FormErrorMessage, FormLabel, Input, Modal, ModalBody, ModalCloseButton, ModalContent, ModalFooter, ModalHeader, ModalOverlay, Select, Stack } from '@chakra-ui/react';
import React, { useState } from 'react';
import RMButton from '../../RMButton';
import Dropdown from '../../Dropdown';

const DeploymentFormModal = ({ isOpen, onSubmit, closeModal }) => {
    const [deployment, setDeployment] = useState({
        Name: '',
        Variables: [],
        Path: [],
        Ports: [],
        DockerImg: '',
        DockerRunArgs: '',
        DockerRunCmd: '',
        GitClones: [],
    });

    const [errors, setErrors] = useState({
        nameError: undefined,
        varNameError: [],
        varTypeError: [],
        varAgentsError: [],
        fromError: [],
        toError: [],
        pathTypeError: [],
        pathAgentsError: [],
        repoError: [],
        branchError: [],
        destError: [],
        dockerImgError: undefined,
        dockerRunArgsError: undefined,
        dockerRunCmdError: undefined,
    });

    const { nameError, varNameError, varTypeError, varAgentsError, fromError, toError, pathTypeError, pathAgentsError, repoError, branchError, destError, dockerImgError, dockerRunArgsError, dockerRunCmdError } = errors;

    const handleChange = (e) => {
        const { name, value } = e.target;
        setDeployment({ ...deployment, [name]: value });
    };

    const handleVariableChange = (index, e) => {
        const { name, value } = e.target;
        const newVariables = [...deployment.Variables];
        newVariables[index][name] = value;
        setDeployment({ ...deployment, Variables: newVariables });
    };

    const handlePathChange = (index, e) => {
        const { name, value } = e.target;
        const newPaths = [...deployment.Path];
        newPaths[index][name] = value;
        setDeployment({ ...deployment, Path: newPaths });
    };

    const handleGitCloneChange = (index, e) => {
        const { name, value } = e.target;
        const newGitClones = [...deployment.GitClones];
        newGitClones[index][name] = value;
        setDeployment({ ...deployment, GitClones: newGitClones });
    };

    const addVariable = () => {
        setDeployment({
            ...deployment,
            Variables: [...deployment.Variables, { Name: '', Type: '', Agents: [] }],
        });
    };

    const addPath = () => {
        setDeployment({
            ...deployment,
            Path: [...deployment.Path, { From: '', To: '', Type: '', Agents: [] }],
        });
    };

    const addGitClone = () => {
        setDeployment({
            ...deployment,
            GitClones: [...deployment.GitClones, { GitRepo: '', GitBranch: '', Dest: '' }],
        });
    };

    const handleSubmit = (e) => {
        e.preventDefault();
        onSubmit(deployment);
        onClose(); // Close the modal after submission
    };

    return (
        <Modal isOpen={isOpen} onClose={closeModal}>
            <ModalOverlay />
            <ModalContent
                className={theme === 'light' ? parentStyles.modalLightContent : parentStyles.modalDarkContent}
                width={isDesktop ? '80%' : isTablet ? '97%' : '99%'}
                maxWidth='none'
                minHeight={'300px'}
                maxH={'90%'}
                textAlign='center'
                overflow='hidden'>
                <ModalHeader
                    whiteSpace='pre-line'
                    className={`${styles.header} ${type === 'error' ? styles.red : styles.orange}`}>
                    {type === 'error' ? `Errors: ${data.length}` : `Warnings: ${data.length}`}
                </ModalHeader>
                <ModalCloseButton />
                <ModalBody className={styles.body}>
                    <Stack>
                        <Box><h2>Create Deployment</h2></Box>
                        <Stack spacing='5'>
                            <FormControl isInvalid={nameError}>
                                <FormLabel htmlFor='name'>Name</FormLabel>
                                <Input id='name' type='text' name='Name' isRequired={false} value={deployment.Name} onChange={handleChange} />
                                <FormErrorMessage>{nameError}</FormErrorMessage>
                            </FormControl>
                            <Stack spacing='5'>
                                <Text>Variables</Text>
                                <Stack>
                                    {deployment.Variables.map((variable, index) => (
                                        <Stack key={index}>
                                            <FormControl isInvalid={varNameError}>
                                                <FormLabel htmlFor={"varname" + index}>Variable Name</FormLabel>
                                                <Input id={"varname" + index} type='text' name='Name' isRequired={false} value={variable.Name} onChange={(e) => handleVariableChange(index, { target: { name: "Name", value: e.target.value } })} />
                                                <FormErrorMessage>{varNameError}</FormErrorMessage>
                                            </FormControl>
                                            <FormControl isInvalid={varTypeError}>
                                                <FormLabel htmlFor={"vartype" + index}>Variable type</FormLabel>
                                                <Dropdown
                                                    id={"vartype" + index}
                                                    isMenuPortalTarget={false}
                                                    selectedValue={variable.Type}
                                                    onChange={(option) => {
                                                        handleVariableChange(index, { target: { name: "Type", value: option } })
                                                    }}
                                                    options={[
                                                        { name: 'Secret', value: 'secret' },
                                                        { name: 'Env', value: 'env' },
                                                    ]}
                                                />
                                                <FormErrorMessage>{varTypeError}</FormErrorMessage>
                                            </FormControl>
                                            <FormControl isInvalid={varAgentsError}>
                                                <FormLabel htmlFor={"varagents" + index}>Agents</FormLabel>
                                                <Input id={"varagents" + index} type='text' name='Agents' isRequired={false} value={variable.Agents.join(',')} onChange={(e) => handleVariableChange(index, { target: { name: 'Agents', value: e.target.value.split(',') } })} />
                                                <FormErrorMessage>{varAgentsError}</FormErrorMessage>
                                            </FormControl>
                                        </Stack>
                                    ))}
                                </Stack>
                                <RMButton colorScheme='blue' size='medium' variant='outline' onClick={addVariable}>Add Variable</RMButton>
                            </Stack>
                            <Stack spacing='5'>
                                <Text>Path Mappings:</Text>
                                <Stack>
                                    {deployment.Path.map((path, index) => (
                                        <Stack key={index}>
                                            <FormControl isInvalid={fromError[index]}>
                                                <FormLabel htmlFor={"from" + index}>From</FormLabel>
                                                <Input id={"from" + index} type='text' name='From' isRequired={false} value={path.From} onChange={(e) => handlePathChange(index, { target: { name: "From", value: e.target.value } })} />
                                                <FormErrorMessage>{fromError[index]}</FormErrorMessage>
                                            </FormControl>
                                            <FormControl isInvalid={toError[index]}>
                                                <FormLabel htmlFor={"to" + index}>To</FormLabel>
                                                <Input id={"to" + index} type='text' name='To' isRequired={true} value={path.To} onChange={(e) => handlePathChange(index, { target: { name: "To", value: e.target.value } })} />
                                                <FormErrorMessage>{toError[index]}</FormErrorMessage>
                                            </FormControl>
                                            <FormControl isInvalid={pathTypeError[index]}>
                                                <FormLabel htmlFor='pathType'>Path type</FormLabel>
                                                <Dropdown
                                                    id={"pathtype" + index}
                                                    isMenuPortalTarget={false}
                                                    selectedValue={path.Type}
                                                    onChange={(option) => {
                                                        handlePathChange(index, { target: { name: "Type", value: option } })
                                                    }}
                                                    options={[
                                                        { name: 'SHM', value: 'shm' },
                                                        { name: 'Direct', value: 'direct' },
                                                    ]}
                                                />
                                                <FormErrorMessage>{pathTypeError[index]}</FormErrorMessage>
                                            </FormControl>
                                            <FormControl isInvalid={pathAgentsError[index]}>
                                                <FormLabel htmlFor='name'>Agents</FormLabel>
                                                <Input id={"pathagents" + index} type='text' name='Agents' isRequired={false} value={path.Agents.join(',')} onChange={(e) => handlePathChange(index, { target: { name: 'Agents', value: e.target.value.split(',') } })} />
                                                <FormErrorMessage>{pathAgentsError[index]}</FormErrorMessage>
                                            </FormControl>
                                        </Stack>
                                    ))}
                                </Stack>
                                <RMButton colorScheme='blue' size='medium' variant='outline' onClick={addPath}>Add Path Mapping</RMButton>
                            </Stack>
                            <Stack spacing='5'>
                                <Text>Git Clones:</Text>
                                <Stack>
                                    {deployment.GitClones.map((clone, index) => (
                                        <Stack key={index}>
                                            <FormControl isInvalid={repoError[index]}>
                                                <FormLabel htmlFor={"repo" + index}>Git Repo</FormLabel>
                                                <Input id={"repo" + index} type='text' name='GitRepo' isRequired={false} value={clone.GitRepo} onChange={(e) => handleGitCloneChange(index, { target: { name: "GitRepo", value: e.target.value } })} />
                                                <FormErrorMessage>{repoError[index]}</FormErrorMessage>
                                            </FormControl>
                                            <FormControl isInvalid={branchError[index]}>
                                                <FormLabel htmlFor={"branch" + index}>Branch</FormLabel>
                                                <Input id={"branch" + index} type='text' name='GitBranch' isRequired={true} value={clone.GitBranch} onChange={(e) => handleGitCloneChange(index, { target: { name: "GitBranch", value: e.target.value } })} />
                                                <FormErrorMessage>{branchError[index]}</FormErrorMessage>
                                            </FormControl>
                                            <FormControl isInvalid={destError[index]}>
                                                <FormLabel htmlFor='dest'>Path type</FormLabel>
                                                <Dropdown
                                                    id={"dest" + index}
                                                    isMenuPortalTarget={false}
                                                    selectedValue={clone.Dest}
                                                    onChange={(option) => {
                                                        handleGitCloneChange(index, { target: { name: "Dest", value: option } })
                                                    }}
                                                    options={[
                                                        { name: 'Config', value: 'config' },
                                                        { name: 'Data', value: 'data' },
                                                    ]}
                                                />
                                                <FormErrorMessage>{destError[index]}</FormErrorMessage>
                                            </FormControl>
                                        </Stack>
                                    ))}
                                </Stack>
                                <RMButton colorScheme='blue' size='medium' variant='outline' onClick={addGitClone}>Add Git Clone</RMButton>
                            </Stack>
                            <FormControl isInvalid={dockerImgError}>
                                <FormLabel htmlFor='dockerImg'>Docker Image</FormLabel>
                                <Input id='dockerImg' type='text' name='DockerImg' isRequired={false} value={deployment.DockerImg} onChange={handleChange} />
                                <FormErrorMessage>{dockerImgError}</FormErrorMessage>
                            </FormControl>
                            <FormControl isInvalid={dockerRunArgsError}>
                                <FormLabel htmlFor='dockerRunArgs'>Docker Run Args</FormLabel>
                                <Input id='dockerRunArgs' type='text' name='DockerRunArgs' isRequired={false} value={deployment.DockerRunArgs} onChange={handleChange} />
                                <FormErrorMessage>{dockerRunArgsError}</FormErrorMessage>
                            </FormControl>
                            <FormControl isInvalid={dockerRunCmdError}>
                                <FormLabel htmlFor='dockerRunCmd'>Docker Run Cmd</FormLabel>
                                <Input id='dockerRunCmd' type='text' name='DockerRunCmd' isRequired={false} value={deployment.DockerRunCmd} onChange={handleChange} />
                                <FormErrorMessage>{dockerRunCmdError}</FormErrorMessage>
                            </FormControl>
                        </Stack>
                    </Stack>
                </ModalBody>
                <ModalFooter gap={3} margin='auto'>
                    <RMButton colorScheme='blue' size='medium' variant='outline' onClick={closeModal}>
                        Cancel
                    </RMButton>
                    <RMButton onClick={handleSubmit} size='medium'>
                        Submit
                    </RMButton>
                </ModalFooter>
            </ModalContent>
        </Modal >
    );
};

export default App;