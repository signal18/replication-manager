import assert from 'node:assert/strict'

import {
  getEncryptionModeLabel,
  getMetadataEncryptionMode,
  getSnapshotEncryptionMode,
} from '../helpers.js'

const runTests = () => {
  assert.equal(getMetadataEncryptionMode(null), null)

  // encrypted=false should always be treated as unencrypted
  assert.equal(getMetadataEncryptionMode({ encrypted: false, encryptionStreamFormat: true }), 'unencrypted')
  assert.equal(getMetadataEncryptionMode({ encrypted: 'false', encryptionStreamFormat: 'stream' }), 'unencrypted')

  // encrypted=true with stream format
  assert.equal(getMetadataEncryptionMode({ encrypted: true, encryptionStreamFormat: true }), 'stream-encrypted')
  assert.equal(getMetadataEncryptionMode({ encrypted: 1, encryptionStreamFormat: 1 }), 'stream-encrypted')
  assert.equal(getMetadataEncryptionMode({ encrypted: 'yes', encryptionStreamFormat: 'stream-transport' }), 'stream-encrypted')

  // encrypted=true with legacy format
  assert.equal(getMetadataEncryptionMode({ encrypted: true, encryptionStreamFormat: false }), 'legacy-encrypted')
  assert.equal(getMetadataEncryptionMode({ encrypted: true, encryptionStreamFormat: 0 }), 'legacy-encrypted')
  assert.equal(getMetadataEncryptionMode({ encrypted: 'true', encryptionStreamFormat: 'legacy' }), 'legacy-encrypted')

  // Unknown format must remain unknown (fail-safe behavior)
  assert.equal(getMetadataEncryptionMode({ encrypted: true, encryptionStreamFormat: 'mystery' }), null)
  assert.equal(getMetadataEncryptionMode({ encrypted: true }), null)

  // Label mapping
  assert.equal(getEncryptionModeLabel('stream-encrypted'), 'Stream Encrypted')
  assert.equal(getEncryptionModeLabel('legacy-encrypted'), 'Legacy Encrypted')
  assert.equal(getEncryptionModeLabel('unencrypted'), 'Unencrypted')
  assert.equal(getEncryptionModeLabel('unknown'), null)

  const snapshot = {
    metadata: [
      { backupMethod: 'logical', encrypted: true, encryptionStreamFormat: true },
      { backupMethod: 'physical', encrypted: true, encryptionStreamFormat: false },
    ],
  }

  // operation-specific preferred method
  assert.equal(getSnapshotEncryptionMode(snapshot, 'logical-backup'), 'stream-encrypted')
  assert.equal(getSnapshotEncryptionMode(snapshot, 'logical-master'), 'stream-encrypted')
  assert.equal(getSnapshotEncryptionMode(snapshot, 'physical-backup'), 'legacy-encrypted')

  // fallback method selection should still work
  const physicalOnlySnapshot = {
    metadata: [{ backupMethod: 'physical', encrypted: true, encryptionStreamFormat: false }],
  }
  assert.equal(getSnapshotEncryptionMode(physicalOnlySnapshot, 'logical-backup'), 'legacy-encrypted')

  console.log('AdvancedReseedModal helpers tests passed')
}

runTests()
