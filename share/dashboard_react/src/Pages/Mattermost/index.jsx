import React, { useEffect, useState } from 'react';
import { useDispatch, useSelector } from 'react-redux';
import { Tabs, TabList, TabPanels, Tab, TabPanel, Box, Textarea, Button } from '@chakra-ui/react';
import { getMeetInfo, readMeetMessages, postMeetMessage } from '../../redux/meetSlice';
import styles from './styles.module.scss';

function MattermostIntegration() {
    const dispatch = useDispatch();
    const { meetInfo, messages, loading, error } = useSelector((state) => state.meet);
    const [channels, setChannels] = useState([]);
    const [message, setMessage] = useState('');
    const [selectedChannel, setSelectedChannel] = useState('');

    useEffect(() => {
        dispatch(getMeetInfo());
    }, [dispatch]);

    useEffect(() => {
        if (meetInfo) {
            const allChannels = [
                ...Object.entries(meetInfo.channel_ids_open).map(([name, id]) => ({ name, id })),
                ...Object.entries(meetInfo.channel_ids_private).map(([name, id]) => ({ name, id })),
                ...Object.entries(meetInfo.channel_ids_direct).map(([name, id]) => ({ name, id })),
            ];
            setChannels(allChannels);
        }
    }, [meetInfo]);

    const handleTabChange = (index) => {
        const channel = channels[index];
        setSelectedChannel(channel.id);
        dispatch(readMeetMessages({ channelId: channel.id }));
    };

    const sendMessage = async () => {
        if (selectedChannel && message) {
            await dispatch(postMeetMessage({ channelId: selectedChannel, message }));
            setMessage('');
            alert('Message sent!');
        }
    };

    return (
        <Box className={styles.mattermostContainer}>
            <h2>Mattermost</h2>
            <Tabs onChange={handleTabChange}>
                <TabList>
                    {channels.map((channel) => (
                        <Tab key={channel.id}>{channel.name}</Tab>
                    ))}
                </TabList>

                <TabPanels>
                    {channels.map((channel) => (
                        <TabPanel key={channel.id}>
                            <Box className={styles.messagesContainer}>
                                {messages && messages.Messages && messages.Messages.map((msg, index) => (
                                    <Box key={index} className={styles.message}>
                                        <strong>{msg.UserId}</strong>: {msg.Message}
                                    </Box>
                                ))}
                            </Box>
                            <Textarea
                                value={message}
                                onChange={(e) => setMessage(e.target.value)}
                                placeholder="Write a message..."
                                className={styles.messageInput}
                            />
                            <Button onClick={sendMessage} className={styles.sendButton}>Send</Button>
                        </TabPanel>
                    ))}
                </TabPanels>
            </Tabs>
        </Box>
    );
}

export default MattermostIntegration;