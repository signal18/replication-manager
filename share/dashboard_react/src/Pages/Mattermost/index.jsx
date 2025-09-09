import styles from './styles.module.scss';
import React, { useEffect, useState, useRef, memo } from 'react';
import { useDispatch, useSelector, shallowEqual } from 'react-redux';
import { Box, Textarea, Button, Drawer, DrawerBody, DrawerHeader, DrawerOverlay, DrawerContent, DrawerCloseButton, Text } from '@chakra-ui/react';
import { getMeetInfo, postMeetMessage, fetchMessages, fetchNewMessages, loadHistoryMessages, viewMessagesOnChannel, uploadFileOnChannel, postJitsiMeeting } from '../../redux/meetSlice';
import { FaPaperPlane } from 'react-icons/fa';
import ChannelTreeView from '../../components/ChannelTreeView';
import FileUploadButton from '../../components/FileUploadButton';
import MessageRender from '../../components/MessageRender';
import Picker from "@emoji-mart/react";
import data from "@emoji-mart/data";

// eslint-disable-next-line react/display-name, react/prop-types
const MattermostIntegration = memo(({ isOpen, setIsChatOpen, onClose, cloud18 }) => {
    const dispatch = useDispatch();
    const { meetInfo, messages, loading} = useSelector((state) => state.meet, shallowEqual);
    const [message, setMessage] = useState('');
    const [selectedChannel, setSelectedChannel] = useState('');
    const [selectedAccordionIndex, setSelectedAccordionIndex] = useState([0]);
    const isTreeViewOpen = selectedAccordionIndex.length > 0;
    const [page, setPage] = useState(0);
    const messagesContainerRef = useRef(null);
    const [scrollPosition, setScrollPosition] = useState(null);
    const [selectedFile, setSelectedFile] = useState(null);
    const [showEmojiPicker, setShowEmojiPicker] = useState(false);

    const isLogged = useSelector((state) => state.auth.isLogged);

    useEffect(() => {
        if (selectedChannel) {
            localStorage.setItem('selectedChannel', selectedChannel);
        }
    }, [selectedChannel]);

    useEffect(() => {
        const savedChannel = localStorage.getItem('selectedChannel');
        if (savedChannel) {
            setSelectedChannel(savedChannel);
            setTimeout(() => {
                if (messagesContainerRef.current) {
                    messagesContainerRef.current.scrollTop = messagesContainerRef.current.scrollHeight;
                }
            }, 1000);
        }
    }, []);

    //to set messages for selected channel for the fist time
    useEffect(() => {
        if (selectedChannel) {
            setPage(0);
            dispatch(fetchMessages({ channelId: selectedChannel, page: 0 }));
            dispatch(viewMessagesOnChannel({ channelId: selectedChannel }));
        }
    }, [dispatch, selectedChannel]);

    //to handle the scroll position
    useEffect(() => {
        if (!messagesContainerRef.current) return;
    
        if (scrollPosition !== null) {
            // Rétablir la position du scroll après chargement des messages
            messagesContainerRef.current.scrollTop =
                messagesContainerRef.current.scrollHeight - scrollPosition;
        } else if (page === 0) {
            // Si c'est le premier chargement, scroll en bas
            messagesContainerRef.current.scrollTop = messagesContainerRef.current.scrollHeight;
        }
    }, [messages, page, scrollPosition]);

    //to update messages every 2 seconds
    useEffect(() => {
        
            const interval = setInterval(() => {
                if (isLogged && cloud18 ){
                    if (selectedChannel) {
                        setScrollPosition(messagesContainerRef.current?.scrollHeight - messagesContainerRef.current?.scrollTop);
                        dispatch(fetchNewMessages({ channelId: selectedChannel })); 
                    }
                    dispatch(getMeetInfo());
                }
            }, 5000);
        return () => clearInterval(interval);
    }, [dispatch, selectedChannel, isLogged, cloud18]);

    if (!isOpen) return null;   
      
    //to handle the scroll event when user reaches the top 
    const handleScroll = () => {
        const container = messagesContainerRef.current;
        if (!container) return;

        if (container.scrollTop === 0 && !loading) {

            setScrollPosition(container.scrollHeight); // to save the scroll position

            setPage((prevPage) => {
                const nextPage = prevPage + 1;
                dispatch(loadHistoryMessages({ channelId: selectedChannel, page: nextPage }));
                return nextPage;
            });
        }
    };

    //to handle the file selected
    const handleFileSelected = (file) => {
        setSelectedFile(file);
    };

    //to send the message to the selected channel
    const handleSendMessage = async () => {
        if (selectedChannel) {
            if (selectedFile) {
                // If a file is selected, upload the file and send a message with it
                const formData = new FormData();
                formData.append('file', selectedFile);
                formData.append('fileName', selectedFile.name);
                formData.append('message', message);

                dispatch(uploadFileOnChannel({ channelId: selectedChannel, formData }));

                setSelectedFile(null);
            } else if (message.trim()) {
                // If no file is selected, just send the message
                dispatch(postMeetMessage({ channelId: selectedChannel, message }));
            }

            setMessage('');
            if (messagesContainerRef.current) {
                messagesContainerRef.current.scrollTop = messagesContainerRef.current.scrollHeight;
            }
        }
    };

    const handleCloseChat = () => {
        setIsChatOpen(false); 
        localStorage.setItem('chatOpen', false);
        setScrollPosition(null);
        onClose();
    };

    const handleCreateMeeting = () => {
        if (!selectedChannel) return;
        const meetingId = crypto.randomUUID();
        dispatch(postJitsiMeeting({ channelId: selectedChannel, meetingId: meetingId }));
    };
    


    return (
        <Drawer isOpen={isOpen} placement="right" onClose={handleCloseChat} size="lg">
            <DrawerOverlay />
            <DrawerContent className={styles.mattermostDrawerContent}> 
                <DrawerCloseButton />
                <DrawerHeader className={styles.mattermostChatHeader}>Support</DrawerHeader>

                <DrawerBody className={styles.mattermostContainer} >
                    <Box className={styles.accordionPanel}>
                        <Box className={styles.treeViewWrapper}>
                            <ChannelTreeView 
                                onSelectChannel={(channel) => { setSelectedChannel(channel); setSelectedAccordionIndex([]);}} 
                                unReadMessagesByChannel={meetInfo?.unread_messages_by_channel || {}} 
                                allUsers={meetInfo?.all_users || {}} 
                                usersStatus={meetInfo?.status_users || {}} 
                                selectedChannel={selectedChannel} 
                                selectedAccordionIndex={selectedAccordionIndex} 
                                setSelectedAccordionIndex={setSelectedAccordionIndex}
                                openChannelToJoin={meetInfo?.channel_ids_open_join || {}}
                            />
                        </Box>
                        {selectedChannel && (
                            <Box
                                className={`${styles.messagesWrapper} ${!isTreeViewOpen ? styles.expanded : ''}`}
                            >
                                <Box className={styles.messagesContainer} ref={messagesContainerRef} onScroll={handleScroll}>
                                    <MessageRender messages={messages[selectedChannel] || null} allUsers={meetInfo?.all_users || {}} />
                                </Box>

                                <Box className={styles.newPost}>
                                    <Textarea
                                        value={message}
                                        onChange={(e) => setMessage(e.target.value)}
                                        placeholder="Write a message..."
                                        className={styles.newPostInput}
                                    />
                                    <Button onClick={() => setShowEmojiPicker(!showEmojiPicker)} className={styles.emojiButton}>
                                        😊
                                    </Button>
                                    {showEmojiPicker && (
                                        <Box position="absolute" bottom="50px" right="10px" zIndex="1000">
                                            <Picker 
                                                data={data} 
                                                onEmojiSelect={(emoji) => setMessage((prevMessage) => prevMessage + emoji.native)}
                                            />
                                        </Box>
                                    )}
                                    <FileUploadButton onFileSelected={handleFileSelected} />  
                                    <Button onClick={handleCreateMeeting} className={styles.meetingButton}>
                                        📹
                                    </Button>
                                    <Button onClick={handleSendMessage} className={styles.newPostSendButton}>
                                        <FaPaperPlane />
                                    </Button>
                                    {selectedFile && (
                                        <Text>Selected File: {selectedFile.name}</Text>
                                    )} 
                                </Box>
                            </Box>
                        )}
                    </Box>
                </DrawerBody>
            </DrawerContent>
        </Drawer>
    
    );
});

export default MattermostIntegration;