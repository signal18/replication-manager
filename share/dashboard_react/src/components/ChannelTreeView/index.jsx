import React, { useState } from 'react';
import { useDispatch, useSelector } from 'react-redux';
import {Accordion, AccordionItem, AccordionButton, AccordionIcon, AccordionPanel,Box, Button, Menu, MenuButton, MenuList, MenuItem, Input, AlertDialog,AlertDialogBody, AlertDialogFooter, AlertDialogHeader, AlertDialogContent, AlertDialogOverlay } from '@chakra-ui/react';
import styles from './styles.module.scss';
import {createDirectChannel, addUserChannel, createPublicChannel, createPrivateChannel, leaveChannel} from '../../redux/meetSlice';
import { FaPlus, FaArrowRight, FaUserPlus, FaCircle } from 'react-icons/fa';

const ChannelTreeView = ({ onSelectChannel, unReadMessagesByChannel, allUsers, usersStatus, selectedChannel = '' , selectedAccordionIndex, setSelectedAccordionIndex }) => {
    const dispatch = useDispatch();
    const [searchTerm, setSearchTerm] = useState('');
    const [channelToLeave, setChannelToLeave] = useState(null);
    const [isOpen, setIsOpen] = useState(false);
    const [isCreateOpen, setIsCreateOpen] = useState(false);
    const [newChannelName, setNewChannelName] = useState('');
    const [channelType, setChannelType] = useState(null);
    const [isAddUserOpen, setIsAddUserOpen] = useState(false);
    const [channelToAddUser, setChannelToAddUser] = useState(null);
    const channels = useSelector((state) => state.meet.channels);

    const groupedChannels = channels.reduce((acc, channel) => {
        if (!acc[channel.type]) {
            acc[channel.type] = [];
        }
        acc[channel.type].push(channel);
        return acc;
    }, {});

    const totalUnreadMessagesByType = Object.entries(groupedChannels).reduce((acc, [type, channels]) => {
        acc[type] = channels.reduce((sum, channel) => sum + (unReadMessagesByChannel[channel.id] || 0), 0);
        return acc;
    }, {});

    const allUsersArray = Object.entries(allUsers).map(([userId, userName]) => ({ id: userId, name: userName }));

    const filteredUsers = allUsersArray.filter(user =>
        user.name.toLowerCase().includes(searchTerm.toLowerCase())
    );

    const handleUserClick = (userId) => {
        dispatch(createDirectChannel({UserId: userId}));
    };


    //Handler to leave channel
    const handleLeaveChannel = (channelId) => {
        setChannelToLeave(channelId);
        setIsOpen(true);
    };

    const confirmLeaveChannel = () => {
        dispatch(leaveChannel({ChannelId: channelToLeave}));
        setIsOpen(false);
    };

    const cancelLeaveChannel = () => {
        setIsOpen(false);
    };


    //Handler to create channel
    const handleCreateChannel = (type) => {
        setChannelType(type);
        setIsCreateOpen(true);
    };

    const confirmCreateChannel = () => {
        if (channelType === 'O') {
            dispatch(createPublicChannel({ ChannelName: newChannelName }));
        } else if (channelType === 'P') {
            dispatch(createPrivateChannel({ ChannelName: newChannelName }));
        }
        setIsCreateOpen(false);
        setNewChannelName('');
    };

    const cancelCreateChannel = () => {
        setIsCreateOpen(false);
        setNewChannelName('');
    };

    //handler to leave channel
    const handleAddUserToChannel = (channelId) => {
        setChannelToAddUser(channelId);
        setIsAddUserOpen(true);
    };

    const confirmAddUserToChannel = (userId) => {
        const channelId = channelToAddUser || "";
        if (channelId) {
            dispatch(addUserChannel({ ChannelId: channelId, UserId: userId }));
        } else {
            console.error("Erreur : channelId est vide !");
        }
        setIsAddUserOpen(false);
    };

    const cancelAddUserToChannel = () => {
        setIsAddUserOpen(false);
    };

    const handleSelectChannel = (channelId) => {
        onSelectChannel(channelId);
        setSelectedAccordionIndex([]);
    };

    return (
    <>
        <Accordion multiple className={styles.channelsContainer} allowMultiple allowToggle index={selectedAccordionIndex} onChange={(index) => setSelectedAccordionIndex(index.length ? index : [])}>
                <AccordionItem className={styles.channelsTreeView}>
                    <AccordionButton className={styles.channelsTreeViewTitle} onClick={() => setSelectedAccordionIndex([0])}>
                        <p>Channels {selectedChannel && `- ${channels.find(c => c.id === selectedChannel)?.name || "Unknown"}`}</p>
                        <AccordionIcon />
                    </AccordionButton>

                    <AccordionPanel className={styles.channelsTreeViewContent}>
                        {Object.entries(groupedChannels).map(([type, channels]) => (
                            <AccordionItem key={type} className={styles.channelsGroup}>
                                <AccordionButton className={styles.channelsTypeButton}>
                                    <p>
                                        {type === 'O' ? 'Public Channels' : type === 'P' ? 'Private Channels' : 'Direct Channels'}
                                        {totalUnreadMessagesByType[type] > 0 && ` (${totalUnreadMessagesByType[type]})`}
                                    </p>
                                    {type !== 'D' && (
                                        <Button
                                            colorScheme="teal"
                                            size="sm"
                                            onClick={() => handleCreateChannel(type)}
                                        >
                                            <FaPlus />
                                        </Button>
                                    )}
                                    {type === 'D' && (
                                        <Box display="flex" alignItems="center">
                                            <Menu>
                                                <MenuButton as={Button} colorScheme="teal" size="sm" ml="auto">
                                                    <FaPlus />
                                                </MenuButton>
                                                <MenuList>
                                                    <Input
                                                        placeholder="Search users..."
                                                        value={searchTerm}
                                                        onChange={(e) => setSearchTerm(e.target.value)}
                                                        mb={2}
                                                    />
                                                    {filteredUsers.map((user) => (
                                                        <MenuItem
                                                            key={user.id}
                                                            onClick={() => handleUserClick(user.id)}
                                                        >
                                                            <Box display="flex" alignItems="center">
                                                                <FaCircle
                                                                    color={usersStatus[user.name] === 'online' ? 'green' : 'gray'}
                                                                    style={{ marginRight: '8px' }}
                                                                />
                                                                {user.name}
                                                            </Box>
                                                        </MenuItem>
                                                    ))}
                                                </MenuList>
                                            </Menu>
                                        </Box>
                                    )}
                                    <AccordionIcon />
                                </AccordionButton>
                                <AccordionPanel className={styles.channelsOfAGroup}>
                                    {channels.map((channel) => (
                                        <Box
                                            key={channel.id}
                                            as='button'
                                            className={styles.channel}
                                            onClick={() => handleSelectChannel(channel.id)}
                                        >
                                            <div className={styles.channelName}>
                                                {type === 'D' && (
                                                    <Box display="flex" alignItems="center">
                                                        <FaCircle
                                                            color={usersStatus[channel.name] === 'online' ? 'green' : 'gray'}
                                                            style={{ marginRight: '8px' }}
                                                        />
                                                        {channel.name}
                                                    </Box>
                                                )}
                                                {type !== 'D' && channel.name}
                                            </div>
                                            <div className={styles.channelUnreadMessages}>{unReadMessagesByChannel && unReadMessagesByChannel[channel.id] > 0 && ` (${unReadMessagesByChannel[channel.id]})`}</div>
                                            {type === 'P' && (
                                                <>
                                                <Button
                                                    colorScheme="red"
                                                    size="sm"
                                                    onClick={(e) => {
                                                        e.stopPropagation();
                                                        handleLeaveChannel(channel.id);
                                                    }}
                                                >
                                                    <FaArrowRight />
                                                </Button>
                                                <Button
                                                    colorScheme="blue"
                                                    size="sm"
                                                    onClick={(e) => {
                                                        e.stopPropagation();
                                                        handleAddUserToChannel(channel.id);
                                                    }}
                                                    ml={2}
                                                >
                                                    <FaUserPlus />
                                                </Button>

                                                </>
                                            )}
                                        </Box>
                                    ))}

                                </AccordionPanel>
                            </AccordionItem>
                        ))}
                    </AccordionPanel>
                
                </AccordionItem>
            </Accordion>
            <AlertDialog isOpen={isOpen} leastDestructiveRef={undefined} onClose={cancelLeaveChannel}>
                <AlertDialogOverlay>
                    <AlertDialogContent>
                        <AlertDialogHeader fontSize='lg' fontWeight='bold'>
                            Leave Channel
                        </AlertDialogHeader>
                        <AlertDialogBody>
                            Are you sure you want to leave this channel?
                        </AlertDialogBody>
                        <AlertDialogFooter>
                            <Button onClick={cancelLeaveChannel}>
                                Cancel
                            </Button>
                            <Button colorScheme='red' onClick={confirmLeaveChannel} ml={3}>
                                Leave
                            </Button>
                        </AlertDialogFooter>
                    </AlertDialogContent>
                </AlertDialogOverlay>
            </AlertDialog>
            <AlertDialog isOpen={isCreateOpen} leastDestructiveRef={undefined} onClose={cancelCreateChannel}>
                <AlertDialogOverlay>
                    <AlertDialogContent>
                        <AlertDialogHeader fontSize='lg' fontWeight='bold'>
                            Create New Channel
                        </AlertDialogHeader>
                        <AlertDialogBody>
                            <Input
                                placeholder="Enter channel name"
                                value={newChannelName}
                                onChange={(e) => setNewChannelName(e.target.value)}
                            />
                        </AlertDialogBody>
                        <AlertDialogFooter>
                            <Button onClick={cancelCreateChannel}>
                                Cancel
                            </Button>
                            <Button colorScheme='teal' onClick={confirmCreateChannel} ml={3}>
                                Create
                            </Button>
                        </AlertDialogFooter>
                    </AlertDialogContent>
                </AlertDialogOverlay>
            </AlertDialog>
            <AlertDialog isOpen={isAddUserOpen} leastDestructiveRef={undefined} onClose={cancelAddUserToChannel}>
                <AlertDialogOverlay>
                    <AlertDialogContent>
                        <AlertDialogHeader fontSize='lg' fontWeight='bold'>
                            Add User to Channel
                        </AlertDialogHeader>
                        <AlertDialogBody>
                            <Box display="flex" alignItems="center">
                                <Menu>
                                    <MenuButton as={Button} colorScheme="teal" size="sm" ml="auto">
                                        <FaUserPlus /> Choose a user
                                    </MenuButton>
                                    <MenuList>
                                        <Input
                                            placeholder="Search users..."
                                            value={searchTerm}
                                            onChange={(e) => setSearchTerm(e.target.value)}
                                            mb={2}
                                        />
                                        {filteredUsers.map((user) => (
                                            <MenuItem
                                                key={user.id}
                                                onClick={() => confirmAddUserToChannel(user.id)}
                                            >
                                                <Box display="flex" alignItems="center">
                                                    <FaCircle
                                                        color={usersStatus[user.name] === 'online' ? 'green' : 'gray'}
                                                        style={{ marginRight: '8px' }}
                                                    />
                                                    {user.name}
                                                </Box>
                                            </MenuItem>
                                        ))}
                                    </MenuList>
                                </Menu>
                            </Box>
                        </AlertDialogBody>
                        <AlertDialogFooter>
                            <Button onClick={cancelAddUserToChannel}>
                                Cancel
                            </Button>
                        </AlertDialogFooter>
                    </AlertDialogContent>
                </AlertDialogOverlay>
            </AlertDialog>
        </>
    );
};

export default ChannelTreeView;