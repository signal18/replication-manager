import React from 'react'
import PropTypes from 'prop-types'
import { Flex } from '@chakra-ui/react'
import { HiQuestionMarkCircle } from 'react-icons/hi'
import TextForm from '../../../components/TextForm'
import NumberInput from '../../../components/NumberInput'
import TableType2 from '../../../components/TableType2'
import RMIconButton from '../../../components/RMIconButton'
import styles from '../styles.module.scss'

const CLUSTER_KEYS = {
  repo: 'prov-app-template-repo',
  branch: 'prov-app-template-repo-branch',
  user: 'prov-app-template-repo-user',
  password: 'prov-app-template-repo-password',
  timeout: 'prov-app-template-repo-timeout'
}

const GLOBAL_KEYS = {
  repo: 'prov-app-template-repo',
  branch: 'prov-app-template-repo-branch',
  user: 'prov-app-template-repo-user',
  password: 'prov-app-template-repo-password',
  timeout: 'prov-app-template-repo-timeout'
}

function AppTemplateRepoSection({ scope, config, canEdit, onSet }) {
  const keys = scope === 'cluster' ? CLUSTER_KEYS : GLOBAL_KEYS
  const h = (tooltip) => (
    <RMIconButton
      icon={HiQuestionMarkCircle}
      tooltip={tooltip}
      aria-label={tooltip}
      iconFontsize='1rem'
      variant='ghost'
      style={{ opacity: 0.5, minWidth: '1.5rem', height: '1.5rem' }}
    />
  )

  const repoVal = config?.provAppTemplateRepo || ''
  const branchVal = config?.provAppTemplateRepoBranch || ''
  const userVal = config?.provAppTemplateRepoUser || ''
  const timeoutVal = config?.provAppTemplateRepoTimeout

  const dataObject = [
    {
      key: 'Repository URL',
      help: h('Git repository URL used to load app templates.'),
      value: (
        <TextForm
          value={repoVal}
          maxLength={1024}
          isDisabled={!canEdit}
          confirmTitle={`Confirm change '${keys.repo}' to:`}
          onSave={(val) => onSet(keys.repo, val)}
        />
      )
    },
    {
      key: 'Branch',
      help: h('Repository branch name used for template lookup.'),
      value: (
        <TextForm
          value={branchVal}
          maxLength={255}
          isDisabled={!canEdit}
          confirmTitle={`Confirm change '${keys.branch}' to:`}
          onSave={(val) => onSet(keys.branch, val)}
        />
      )
    },
    {
      key: 'Username',
      help: h('Repository username for authenticated access.'),
      value: (
        <TextForm
          value={userVal}
          maxLength={255}
          isDisabled={!canEdit}
          confirmTitle={`Confirm change '${keys.user}' to:`}
          onSave={(val) => onSet(keys.user, val)}
        />
      )
    },
    {
      key: 'Password',
      help: h('Repository password/token. Saved securely after encoding.'),
      value: (
        <TextForm
          type='password'
          value=''
          placeholder='Set new password'
          maxLength={2048}
          isDisabled={!canEdit}
          confirmTitle={`Confirm update '${keys.password}'?`}
          confirmBody='Are you sure you want to update this secret value to:'
          onSave={(val) => onSet(keys.password, btoa(val))}
        />
      )
    },
    {
      key: 'Timeout (seconds)',
      help: h('HTTP timeout used when fetching templates from the repository.'),
      value: (
        <NumberInput
          min={1}
          max={600}
          value={timeoutVal}
          isDisabled={!canEdit}
          showEditButton={true}
          showConfirmModal={true}
          confirmTitle={`Confirm change '${keys.timeout}' to:`}
          onConfirm={(value) => onSet(keys.timeout, value)}
        />
      )
    }
  ]

  return (
    <Flex justify='space-between' gap='0'>
      <TableType2 dataArray={dataObject} className={styles.tableWithHelp} helpColumn={true} />
    </Flex>
  )
}

AppTemplateRepoSection.propTypes = {
  scope: PropTypes.oneOf(['cluster', 'global']).isRequired,
  config: PropTypes.object,
  canEdit: PropTypes.bool,
  onSet: PropTypes.func.isRequired
}

export default AppTemplateRepoSection
