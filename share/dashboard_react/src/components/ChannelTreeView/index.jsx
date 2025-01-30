import React from 'react';
import { Box, Text } from '@chakra-ui/react';
import styles from './styles.module.scss';

const ChannelTreeView = ({ channels, onSelectChannel }) => {
    const getChannelColor = (type) => {
        switch (type) {
            case 'O':
                return 'green';
            case 'P':
                return 'blue';
            case 'D':
                return 'red';
            default:
                return 'gray';
        }
    };

    const groupedChannels = channels.reduce((acc, channel) => {
        if (!acc[channel.type]) {
            acc[channel.type] = [];
        }
        acc[channel.type].push(channel);
        return acc;
    }, {});

    return (
        <Box className={styles.treeViewContainer} overflowY="auto" maxHeight="400px">
            {Object.entries(groupedChannels).map(([type, channels]) => (
                <Box key={type} className={styles.channelGroup}>
                    <Text className={styles.channelType} style={{ color: getChannelColor(type) }}>
                        {type === 'O' ? 'Open Channels' : type === 'P' ? 'Private Channels' : 'Direct Channels'}
                    </Text>
                    {channels.map((channel) => (
                        <Box
                            key={channel.id}
                            className={styles.treeViewItem}
                            onClick={() => onSelectChannel(channel.id)}
                            style={{ color: getChannelColor(type) }}
                        >
                            <Text>{channel.name}</Text>
                        </Box>
                    ))}
                </Box>
            ))}
        </Box>
    );
};

export default ChannelTreeView;