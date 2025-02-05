import React from 'react';
import { Accordion, AccordionItem, AccordionButton, AccordionIcon, AccordionPanel, Box } from '@chakra-ui/react';
import styles from './styles.module.scss';

const ChannelTreeView = ({ channels, onSelectChannel, unReadMessagesByChannel }) => {
    const groupedChannels = channels.reduce((acc, channel) => {
        if (!acc[channel.type]) {
            acc[channel.type] = [];
        }
        acc[channel.type].push(channel);
        return acc;
    }, {});

    return (
        <Accordion multiple className={styles.channelsContainer} allowMultiple allowToggle>
            <AccordionItem className={styles.channelsTreeView} allowToggle>
                <AccordionButton className={styles.channelsTreeViewTitle}>
                    <p>Channels</p>
                    <AccordionIcon />
                </AccordionButton>
                <AccordionPanel className={styles.channelsTreeViewContent}>
                    {Object.entries(groupedChannels).map(([type, channels]) => (
                        <AccordionItem key={type} className={styles.channelsGroup}>
                            <AccordionButton className={styles.channelsTypeButton}>
                                <p>{type === 'O' ? 'Public Channels' : type === 'P' ? 'Private Channels' : 'Direct Channels'}</p>
                                <AccordionIcon />
                            </AccordionButton>
                            <AccordionPanel className={styles.channelsOfAGroup}>
                                {channels.map((channel) => (
                                    <Box
                                        key={channel.id}
                                        as='button'
                                        className={styles.channel}
                                        onClick={() => onSelectChannel(channel.id)}
                                    >
                                        <div className={styles.channelName}>{channel.name}</div>
                                        <div className={styles.channelUnreadMessages}>{unReadMessagesByChannel && unReadMessagesByChannel[channel.id] > 0 && ` (${unReadMessagesByChannel[channel.id]})`}</div>
                                    </Box>
                                ))}
                            </AccordionPanel>
                        </AccordionItem>
                    ))}
                </AccordionPanel>
            </AccordionItem>
        </Accordion>
    );
};

export default ChannelTreeView;