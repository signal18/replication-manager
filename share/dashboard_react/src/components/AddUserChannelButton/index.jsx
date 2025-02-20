import React, { useState } from 'react';
import { useDispatch } from 'react-redux';
import { Input, MenuList, MenuItem, MenuButton, Menu,Box,Button, AlertDialog,AlertDialogBody, AlertDialogFooter, AlertDialogHeader, AlertDialogContent, AlertDialogOverlay } from '@chakra-ui/react';
import styles from './styles.module.scss';
import { addUserChannel} from '../../redux/meetSlice';
import { FaUserPlus, FaCircle} from 'react-icons/fa';

const AddUserChannelButton = ({ selectedChannel, allUsers, usersStatus }) => {
    const dispatch = useDispatch();
    const [isAddUserOpen, setIsAddUserOpen] = useState(false);
    const [channelToAddUser, setChannelToAddUser] = useState(null);
    const [searchTerm, setSearchTerm] = useState('');

    const allUsersArray = Object.entries(allUsers).map(([userId, userName]) => ({ id: userId, name: userName }));

    const filteredUsers = allUsersArray.filter(user =>
        user.name.toLowerCase().includes(searchTerm.toLowerCase())
    );

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

    return (
    <>
        <Button
            colorScheme="blue"
            size="sm"
            onClick={(e) => {
                e.stopPropagation();
                handleAddUserToChannel(selectedChannel);
            }}
            ml={2}
        >
            <FaUserPlus />
        </Button>
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

export default AddUserChannelButton;