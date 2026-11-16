import { useState } from 'react'
import { Box, Code, Container, Flex, Heading, Stack, Text } from '@chakra-ui/react'
import PageContainer from '../PageContainer'
import RMButton from '../../components/RMButton'
import { authService } from '../../services/authService'

// A one-time secret link (/reveal#<code>), mailed by replication-manager: a rotated password or
// credentials. No login. The code stays in the URL fragment (never sent to the server or a proxy)
// and is taken out of the address bar at once; the secret is fetched only on the click, so a mail
// scanner opening the link shows nothing and does not burn it. The server deletes it at the first open.
function readCode() {
  const code = window.location.hash.replace(/^#/, '').trim()
  if (code) {
    window.history.replaceState(null, '', window.location.pathname)
  }
  return code
}

function Reveal() {
  const [code] = useState(readCode)
  const [state, setState] = useState(code ? 'ready' : 'nocode')
  const [reveal, setReveal] = useState(null)
  const [error, setError] = useState('')
  const [copied, setCopied] = useState('')

  const open = async () => {
    setState('opening')
    try {
      const res = await authService.openSecretReveal(code)
      if (res.status === 200 && res.data?.items) {
        setReveal(res.data)
        setState('shown')
        return
      }
      setError(typeof res.data === 'string' && res.data ? res.data : 'This link could not be opened.')
      setState(res.status === 404 ? 'gone' : 'error')
    } catch (e) {
      setError('The server could not be reached. Try again later: the link still works until it is opened.')
      setState('error')
    }
  }

  const copy = async (label, value) => {
    try {
      await navigator.clipboard.writeText(value)
      setCopied(label)
    } catch (e) {
      setCopied('')
    }
  }

  return (
    <PageContainer>
      <Container maxWidth='lg' py={{ base: '16', md: '24' }} px={{ base: '4', sm: '8' }}>
        <Stack spacing='6'>
          <Heading size='md'>{reveal?.title || 'One-time secret link'}</Heading>
          {state === 'nocode' && <Text>This address has no link code. Open the link from your mail as it is.</Text>}
          {(state === 'ready' || state === 'opening') && (
            <>
              <Text>
                This link shows a secret <b>once</b>. After you click, it is gone from the server: copy it to a safe place
                (a password manager) before you leave this page.
              </Text>
              <Box>
                <RMButton onClick={open} isLoading={state === 'opening'}>Show the secret</RMButton>
              </Box>
            </>
          )}
          {state === 'shown' && (
            <>
              {reveal.cluster && <Text fontSize='sm'>Cluster: {reveal.cluster}</Text>}
              {reveal.items.map((it) => (
                <Flex key={it.label} gap='3' align='center' wrap='wrap'>
                  <Text minW='100px' fontWeight='bold'>{it.label}</Text>
                  <Code p='2' wordBreak='break-all'>{it.value}</Code>
                  <RMButton size='sm' onClick={() => copy(it.label, it.value)}>{copied === it.label ? 'Copied' : 'Copy'}</RMButton>
                </Flex>
              ))}
              <Text fontSize='sm'>This link no longer works: reloading this page will not show the secret again.</Text>
            </>
          )}
          {state === 'gone' && (
            <Text>
              This link was already opened, has expired or does not exist. If you did not open it yourself, someone else may
              have seen the secret: tell your administrator at once.
            </Text>
          )}
          {state === 'error' && <Text>{error}</Text>}
        </Stack>
      </Container>
    </PageContainer>
  )
}

export default Reveal
