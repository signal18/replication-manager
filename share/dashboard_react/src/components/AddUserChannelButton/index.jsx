import React, { useState } from 'react';
import { useDispatch } from 'react-redux';
import { Input, MenuList, MenuItem, MenuButton, Menu,Box,Button, AlertDialog,AlertDialogBody, AlertDialogFooter, AlertDialogHeader, AlertDialogContent, AlertDialogOverlay, Text } from '@chakra-ui/react';
import styles from './styles.module.scss';
import { addUserChannel} from '../../redux/meetSlice';
import { FaUserPlus, FaCircle} from 'react-icons/fa';

const AddUserChannelButton = ({ selectedChannel, allUsers, usersStatus }) => {
    const dispatch = useDispatch();
    const [isAddUserOpen, setIsAddUserOpen] = useState(false);
    const [searchTerm, setSearchTerm] = useState('');
    const [selectedUser, setSelectedUser] = useState(null);

    const allUsersArray = Object.entries(allUsers).map(([userId, userName]) => ({ id: userId, name: userName }));

    const filteredUsers = allUsersArray.filter(user =>
        user.name.toLowerCase().includes(searchTerm.toLowerCase())
    );

    const handleAddUserToChannel = () => {
        setIsAddUserOpen(true);
    };

    const confirmAddUserToChannel = (userId) => {
        if (selectedChannel) {
            dispatch(addUserChannel({ ChannelId: selectedChannel, UserId: userId }));
        } else {
            console.error("Erreur : channelId est vide !");
        }
        setIsAddUserOpen(false);
    };

    const cancelAddUserToChannel = () => {
        setIsAddUserOpen(false);
    };

    const handleUserSelect = (user) => {
        setSelectedUser(user); // Stocke l'utilisateur sélectionné
    };

    const handleConfirm = () => {
        if (selectedUser) {
            confirmAddUserToChannel(selectedUser.id);
            setSelectedUser(null); // Réinitialise après confirmation
        }
    };

    return (
    <>
        <Button
            colorScheme="blue"
            size="sm"
            onClick={(e) => {
                e.stopPropagation();
                handleAddUserToChannel();
            }}
            ml={2}
        >
            <FaUserPlus />
        </Button>
        <AlertDialog isOpen={isAddUserOpen} leastDestructiveRef={undefined} onClose={cancelAddUserToChannel}>
            <AlertDialogOverlay>
                <AlertDialogContent>
                    <AlertDialogHeader fontSize='lg' fontWeight='bold' color='black'>
                        Add User to Channel
                    </AlertDialogHeader>
                    <AlertDialogBody>
                        <Box display="flex" flexDirection="column" alignItems="center" justifyContent="center" gap={3}>
                            <Menu>
                                <MenuButton as={Button} colorScheme="teal" size="lg">
                                    <FaUserPlus /> Choose a user
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
                                            onClick={() => handleUserSelect(user)}
                                        >
                                            <Box display="flex" alignItems="center" color='black'>
                                                <FaCircle
                                                    color={usersStatus?.[user.name] === 'online' ? 'green' : 'gray'}
                                                    style={{ marginRight: '8px' }}
                                                />
                                                {user.name}
                                            </Box>
                                        </MenuItem>
                                    ))}
                                </MenuList>
                            </Menu>
                            {selectedUser && (
                                <Text fontSize="md" fontWeight="bold" sx={{ color: 'black !important' }} mt={2}>
                                    Selected User: {selectedUser.name}
                                </Text>
                            )}
                        </Box>
                    </AlertDialogBody>
                    <AlertDialogFooter>
                        <Button onClick={cancelAddUserToChannel}>
                            Cancel
                        </Button>
                        <Button 
                            colorScheme="teal" 
                            onClick={handleConfirm} 
                            ml={3} 
                            isDisabled={!selectedUser} // Désactive le bouton si aucun utilisateur n'est sélectionné
                        >
                            Confirm
                        </Button>
                    </AlertDialogFooter>
                </AlertDialogContent>
            </AlertDialogOverlay>
        </AlertDialog>
    </>
    );
};

export default AddUserChannelButton;