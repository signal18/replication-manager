import styles from './styles.module.scss';
import React, { useEffect, useState, useRef } from 'react';
import { useDispatch, useSelector } from 'react-redux';
import { Input, Box, Textarea, Button, Drawer, DrawerBody, DrawerFooter, DrawerHeader, DrawerOverlay, DrawerContent, DrawerCloseButton } from '@chakra-ui/react';
import { getMeetInfo, postMeetMessage, fetchMessages, fetchNewMessages, loadHistoryMessages, viewMessagesOnChannel } from '../../redux/meetSlice';
import ChannelTreeView from '../../components/ChannelTreeView';


function MattermostIntegration({ isOpen, onClose }) {
    if (!isOpen) return null;
    const dispatch = useDispatch();
    const { meetInfo, messages, loading, error } = useSelector((state) => state.meet);
    const [channels, setChannels] = useState([]);
    const [message, setMessage] = useState('');
    const [selectedChannel, setSelectedChannel] = useState('');
    const [page, setPage] = useState(0);
    const messagesContainerRef = useRef(null);
    const [scrollPosition, setScrollPosition] = useState(null);

    //to get the meet info
    useEffect(() => {
        dispatch(getMeetInfo());
    }, [dispatch]);

    //to get the channels from meet info
    useEffect(() => {
        if (meetInfo) {
            const allChannels = [
                ...Object.entries(meetInfo.channel_ids_open).map(([name, id]) => ({ name, id, type: 'O' })),
                ...Object.entries(meetInfo.channel_ids_private).map(([name, id]) => ({ name, id, type: 'P' })),
                ...Object.entries(meetInfo.channel_ids_direct).map(([name, id]) => ({ name, id, type: 'D' })),
            ];
            setChannels(allChannels);
        }
    }, [meetInfo]);

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
    }, [messages]);

    //to update messages every 3 seconds
    useEffect(() => {
        const interval = setInterval(() => {
            if (selectedChannel) {
                setScrollPosition(messagesContainerRef.current?.scrollHeight - messagesContainerRef.current?.scrollTop);
                dispatch(fetchNewMessages({ channelId: selectedChannel }));
                dispatch(getMeetInfo());
            }
        }, 2000);

        return () => clearInterval(interval);
    }, [dispatch, selectedChannel]);

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

    //to send the message to the selected channel
    const sendMessage = async () => {
        if (selectedChannel && message) {
            await dispatch(postMeetMessage({ channelId: selectedChannel, message }));
            setMessage('');
            // Scroller vers le bas après l'envoi du message
            if (messagesContainerRef.current) {
                messagesContainerRef.current.scrollTop = messagesContainerRef.current.scrollHeight;
            }
        }
    };

    //////////////////////////////////////
    //Messages Render Functions//////////
    const getUserName = (userId) => {
        return meetInfo.all_users[userId] || userId;
    };

    const formatTime = (timestamp) => {
        const date = new Date(timestamp);
        return date.toLocaleTimeString(); // Format the time as a readable string
    };

    const formatDate = (timestamp) => {
        const date = new Date(timestamp);
        return date.toLocaleDateString(); // Format the date as a readable string
    };

    const renderMessages = () => {
        if (!messages[selectedChannel]) return null;

        let lastDate = '';

        return messages[selectedChannel].slice().reverse().map((msg, index) => {
            const messageDate = formatDate(msg.CreateAt);
            const shouldShowDate = messageDate !== lastDate;

            if (shouldShowDate) {
                lastDate = messageDate;
            }

            return (
                <React.Fragment key={index}>
                    {shouldShowDate && (
                        <Box className={styles.dateSeparator}>
                            {messageDate}
                        </Box>
                    )}
                    <Box key={index} className={styles.post}>
                        <div className={styles.postUser}>{getUserName(msg.UserId)} {formatTime(msg.CreateAt)}</div>
                        <div className={styles.postContent}>{msg.Message}</div>
                    </Box>
                </React.Fragment>
            );
        });
    };
    //////////////////////////////////

    /////////////////////////////////
    //File Upload Button Component//
    const FileUploadButton = ({ onFileSelected }) => {
        const fileInputRef = useRef(null);
      
        const handleButtonClick = () => {
          fileInputRef.current.click();
        };
      
        const handleFileChange = (event) => {
          const file = event.target.files[0];
          if (file) {
            onFileSelected(file);
          }
        };
      
        return (
          <>
            <Input
              type="file"
              ref={fileInputRef}
              onChange={handleFileChange}
              style={{ display: 'none' }}
            />
            <Button onClick={handleButtonClick}>
              Upload File
            </Button>
          </>
        );
    };
    /////////////////////////////////

    return (
        <Drawer isOpen={isOpen} placement="right" onClose={onClose} size="lg">
            <DrawerOverlay />
            <DrawerContent className={styles.mattermostDrawerContent}> 
                <DrawerCloseButton />
                <DrawerHeader className={styles.mattermostChatHeader}>Support</DrawerHeader>

                <DrawerBody className={styles.mattermostContainer} >
                    <Box className={styles.accordionPanel}>
                        <Box className={styles.treeViewWrapper}>
                            <ChannelTreeView channels={channels} onSelectChannel={setSelectedChannel} unReadMessagesByChannel={meetInfo?.unread_messages_by_channel || {}} />
                        </Box>
                        <Box
                            className={styles.messagesWrapper}
                        >
                            <Box className={styles.messagesContainer} ref={messagesContainerRef} onScroll={handleScroll}>
                                {renderMessages()}
                            </Box>

                            <Box className={styles.newPost}>
                                <Textarea
                                    value={message}
                                    onChange={(e) => setMessage(e.target.value)}
                                    placeholder="Write a message..."
                                    className={styles.newPostInput}
                                />
                                <Button onClick={sendMessage} className={styles.newPostSendButton}>
                                    <svg xmlns="http://www.w3.org/2000/svg" version="1.1" width="18" height="18" fill="currentColor" viewBox="0 0 24 24">
                                        <path d="M2,21L23,12L2,3V10L17,12L2,14V21Z"></path>
                                    </svg>
                                </Button>
                            </Box>
                        </Box>
                    </Box>
                </DrawerBody>
            </DrawerContent>
        </Drawer>
    
    );
}

export default MattermostIntegration;