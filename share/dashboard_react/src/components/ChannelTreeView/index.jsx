import React, { useState } from 'react';
import { useDispatch, useSelector } from 'react-redux';
import {Accordion, AccordionItem, AccordionButton, AccordionIcon, AccordionPanel,Box, Button, Menu, MenuButton, MenuList, MenuItem, Input, AlertDialog,AlertDialogBody, AlertDialogFooter, AlertDialogHeader, AlertDialogContent, AlertDialogOverlay } from '@chakra-ui/react';
import styles from './styles.module.scss';
import {createDirectChannel, createPublicChannel, createPrivateChannel} from '../../redux/meetSlice';
import { FaPlus, FaCircle } from 'react-icons/fa';
import LeaveUserChannelButton from '../LeaveUserChannelButton';
import AddUserChannelButton from '../AddUserChannelButton';

const ChannelTreeView = ({ onSelectChannel, unReadMessagesByChannel, allUsers, usersStatus, selectedChannel = '' , selectedAccordionIndex, setSelectedAccordionIndex }) => {
    const dispatch = useDispatch();
    const [searchTerm, setSearchTerm] = useState('');
    const [isCreateOpen, setIsCreateOpen] = useState(false);
    const [newChannelName, setNewChannelName] = useState('');
    const [channelType, setChannelType] = useState(null);
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

    const handleSelectChannel = (channelId) => {
        onSelectChannel(channelId);
        setSelectedAccordionIndex([]);
    };

    return (
    <>
        <Accordion multiple className={styles.channelsContainer} allowMultiple allowToggle index={selectedAccordionIndex} onChange={(index) => setSelectedAccordionIndex(index.length ? index : [])}>
                <AccordionItem className={styles.channelsTreeView}>
                    <AccordionButton className={styles.channelsTreeViewTitle} onClick={() => setSelectedAccordionIndex([0])}>
                        <AccordionIcon />

                        <Box className={styles.channelsType}>
                            <p className={styles.channelName}>
                                Channels {selectedChannel && `: ${channels.find(c => c.id === selectedChannel)?.name || ""}`}
                            </p>
                            {selectedChannel && (
                                <Box className={styles.channelsTypeButton}>
                                    {channels.find(c => c.id === selectedChannel)?.type === 'P' && (
                                        <>
                                            <LeaveUserChannelButton selectedChannel={selectedChannel} onSelectChannel={onSelectChannel} />
                                            <AddUserChannelButton selectedChannel={selectedChannel} allUsers={allUsers} usersStatus={usersStatus}/>
                                        </>
                                    )}
                                    {channels.find(c => c.id === selectedChannel)?.type === 'D' && (
                                    <>
                                        <FaCircle
                                            color={usersStatus[channels.find(c => c.id === selectedChannel)?.name] === 'online' ? 'green' : 'gray'}
                                            style={{ marginRight: '8px' }}
                                        />
                                    </>
                                )}
                                </Box>
                            )}
                        </Box>
                        
                    </AccordionButton>

                    <AccordionPanel className={styles.channelsTreeViewContent}>
                        {Object.entries(groupedChannels).map(([type, channels]) => (
                            <AccordionItem key={type} className={styles.channelsGroup}>
                                <AccordionButton className={styles.channelsType}>
                                    <p>
                                        {type === 'O' ? 'Public Channels' : type === 'P' ? 'Private Channels' : 'Direct Channels'}
                                        {totalUnreadMessagesByType[type] > 0 && ` (${totalUnreadMessagesByType[type]})`}
                                    </p>
                                    {type !== 'D' && (
                                        <Button
                                            onClick={() => handleCreateChannel(type)}
                                            className={styles.channelsTypeButton}
                                        >
                                            <FaPlus />
                                        </Button>
                                    )}
                                    {type === 'D' && (
                                        <Box >
                                            <Menu>
                                                <MenuButton as={Button} className={styles.channelsTypeButton}>
                                                    <FaPlus/>
                                                </MenuButton>
                                                <MenuList>
                                                    <Input
                                                        placeholder="Search users..."
                                                        value={searchTerm}
                                                        onChange={(e) => setSearchTerm(e.target.value)}
                                                        mb={2}
                                                        sx={{ color: 'black !important' }}
                                                    />
                                                    {filteredUsers.map((user) => (
                                                        <MenuItem
                                                            key={user.id}
                                                            onClick={() => handleUserClick(user.id)}
                                                        >
                                                            <Box display="flex" alignItems="center" color='black'>
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
                                                <Box className={styles.channelActionsButtons}>
                                                    <LeaveUserChannelButton selectedChannel={channel.id} />
                                                    <AddUserChannelButton selectedChannel={channel.id} allUsers={allUsers} usersStatus={usersStatus}/>
                                                </Box>
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
            <AlertDialog isOpen={isCreateOpen} leastDestructiveRef={undefined} onClose={cancelCreateChannel}>
                <AlertDialogOverlay>
                    <AlertDialogContent>
                        <AlertDialogHeader fontSize='lg' fontWeight='bold' color='black'>
                            Create New Channel
                        </AlertDialogHeader>
                        <AlertDialogBody>
                            <Input
                                placeholder="Enter channel name"
                                value={newChannelName}
                                onChange={(e) => setNewChannelName(e.target.value)}
                                sx={{ color: 'black !important' }}
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
        </>
    );
};

export default ChannelTreeView;
