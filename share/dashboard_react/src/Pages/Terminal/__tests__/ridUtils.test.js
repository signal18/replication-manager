import assert from 'node:assert/strict'

import {
  normalizeRIDSearchParams,
  OpenSVCTerminalRID,
  resolveServiceContainerFromRID,
  setRIDSearchParam,
} from '../ridUtils.js'

const runTests = () => {
  assert.equal(resolveServiceContainerFromRID(null), OpenSVCTerminalRID.Default)
  assert.equal(resolveServiceContainerFromRID('container#db'), OpenSVCTerminalRID.Default)
  assert.equal(resolveServiceContainerFromRID('container#jobs'), OpenSVCTerminalRID.Jobs)
  assert.equal(resolveServiceContainerFromRID('invalid'), OpenSVCTerminalRID.Default)

  {
    const { params, changed } = normalizeRIDSearchParams(new URLSearchParams('rid=container%23jobs'), false)
    assert.equal(changed, true)
    assert.equal(params.get('rid'), null)
  }

  {
    const { params, changed } = normalizeRIDSearchParams(new URLSearchParams('rid=container%23jobs'), true)
    assert.equal(changed, false)
    assert.equal(params.get('rid'), OpenSVCTerminalRID.Jobs)
  }

  {
    const { params, changed } = normalizeRIDSearchParams(new URLSearchParams('rid=container%23db'), true)
    assert.equal(changed, true)
    assert.equal(params.get('rid'), null)
  }

  {
    const { params, changed } = normalizeRIDSearchParams(new URLSearchParams('rid=invalid'), true)
    assert.equal(changed, true)
    assert.equal(params.get('rid'), null)
  }

  {
    const nextParams = setRIDSearchParam(new URLSearchParams('foo=bar'), OpenSVCTerminalRID.Jobs)
    assert.equal(nextParams.get('foo'), 'bar')
    assert.equal(nextParams.get('rid'), OpenSVCTerminalRID.Jobs)
  }

  {
    const nextParams = setRIDSearchParam(new URLSearchParams('foo=bar&rid=container%23jobs'), OpenSVCTerminalRID.Default)
    assert.equal(nextParams.get('foo'), 'bar')
    assert.equal(nextParams.get('rid'), null)
  }

  console.log('ridUtils tests passed')
}

runTests()
