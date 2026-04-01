import { Box, Flex } from '@chakra-ui/react'
import { useState } from 'react'
import PropTypes from 'prop-types'
import CommonModal from '../../components/Modals/CommonModal'
import modalStyles from '../../components/Modals/styles.module.scss'
import Markdown from 'react-markdown'
import remarkGfm from 'remark-gfm'
import LogsSettingsSections from './LogsSettingsSections'

function LogsSettings({ selectedCluster, user }) {
  const [action, setAction] = useState({ title: '', body: <></> })
  const [isCommonModalOpen, setIsCommonModalOpen] = useState(false)

  const openInfoModal = (title, content) => {
    setAction({ title, body: <Box className={modalStyles.infoTooltip}><Markdown remarkPlugins={[remarkGfm]}>{content}</Markdown></Box> })
    setIsCommonModalOpen(true)
  }

  return (
    <>
      <Flex justify='space-between' gap='0' direction='column'>
        <LogsSettingsSections selectedCluster={selectedCluster} user={user} onOpenInfoModal={openInfoModal} />
      </Flex>
      <CommonModal isOpen={isCommonModalOpen} closeModal={() => setIsCommonModalOpen(false)} title={action.title} body={action.body} size='xl' />
    </>
  )
}

LogsSettings.propTypes = {
  selectedCluster: PropTypes.object,
  user: PropTypes.object
}

export default LogsSettings
