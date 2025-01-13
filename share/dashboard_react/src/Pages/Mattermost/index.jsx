import { Flex } from '@chakra-ui/react'
import React, { useEffect, useState } from 'react'
import styles from './styles.module.scss'
import { meetService } from '../../services/meetService';
import AccordionComponent from '../../components/AccordionComponent'
import ConfirmModal from '../../components/Modals/ConfirmModal'

function MattermostIntegration({}) {
    const [channels, setChannels] = useState([]);
    const [message, setMessage] = useState('');
    const [selectedChannel, setSelectedChannel] = useState('');

    useEffect(() => {
        const fetchChannels = async () => {
            const data = await meetService.getMeetPublicChannels();
            setChannels(data);
        };
        fetchChannels();
    }, []);

    const sendMessage = async () => {
        if (selectedChannel && message) {
            await meetService.postMeetMessageOnChannel(selectedChannel, message);
            alert('Message send !');
        }
    };

    return (
        <Flex className={styles.mattermostContainer}>
        <AccordionComponent
            heading={'Mattermost'}
            headerClassName={styles.accordionHeader}
            panelClassName={styles.accordionPanel}
            body={
                <div>
                    <h2>Mattermost</h2>
                    <select onChange={(e) => setSelectedChannel(e.target.value)} className={styles.channelSelect}>
                        <option value="">Choose a channel</option>
                        {channels.map((channel) => (
                            <option key={channel.id} value={channel.id}>
                                {channel.display_name}
                            </option>
                        ))}
                    </select>
                    <textarea
                        value={message}
                        onChange={(e) => setMessage(e.target.value)}
                        placeholder="Write a message..."
                        className={styles.messageInput}
                    />
                    <button onClick={sendMessage} className={styles.sendButton}>Send</button>
                </div>
            }
        />
        </Flex>
    );
};

export default MattermostIntegration;
