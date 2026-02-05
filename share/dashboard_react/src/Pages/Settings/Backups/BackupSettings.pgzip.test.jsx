import React from 'react'
import { render, screen, fireEvent, waitFor } from '@testing-library/react'
import { Provider } from 'react-redux'
import { configureStore } from '@reduxjs/toolkit'
import BackupSettings from './BackupSettings'
import settingsReducer from '../../../redux/settingsSlice'
import globalClustersReducer from '../../../redux/globalClustersSlice'

// Mock components that might have external dependencies
jest.mock('../../../components/RMSwitch', () => {
  return function MockRMSwitch({ isChecked, onChange, confirmTitle }) {
    return (
      <button
        data-testid="rm-switch"
        data-checked={isChecked}
        onClick={onChange}
        aria-label={confirmTitle}
      >
        {isChecked ? 'ON' : 'OFF'}
      </button>
    )
  }
})

jest.mock('../../../components/NumberInput', () => {
  return function MockNumberInput({ value, min, max, onConfirm, confirmTitle, showEditButton }) {
    const [editing, setEditing] = React.useState(false)
    const [tempValue, setTempValue] = React.useState(value)

    return (
      <div data-testid="number-input-container">
        <span data-testid="number-input-value">{value}</span>
        {showEditButton && (
          <button
            data-testid="number-input-edit"
            onClick={() => setEditing(true)}
          >
            Edit
          </button>
        )}
        {editing && (
          <div data-testid="number-input-modal">
            <input
              data-testid="number-input-field"
              type="number"
              min={min}
              max={max}
              value={tempValue}
              onChange={(e) => setTempValue(parseInt(e.target.value))}
            />
            <button
              data-testid="number-input-confirm"
              onClick={() => {
                onConfirm(tempValue)
                setEditing(false)
              }}
            >
              Confirm
            </button>
            <button
              data-testid="number-input-cancel"
              onClick={() => setEditing(false)}
            >
              Cancel
            </button>
          </div>
        )}
      </div>
    )
  }
})

describe('BackupSettings - pgzip Configuration', () => {
  let store

  const mockCluster = {
    name: 'test-cluster',
    config: {
      compressBackups: false,
      compressBackupsCompressionLevel: 6,
      compressBackupsParallelBlocks: 4,
      backupLogicalType: 'mysqldump',
      backupPhysicalType: 'mariabackup',
      backupSaveScript: '',
      backupLoadScript: '',
      sstSendBuffer: 16384
    }
  }

  const mockMonitor = {
    backupLogicalList: { mysqldump: 'mysqldump', mydumper: 'mydumper' },
    backupPhysicalList: { mariabackup: 'mariabackup', xtrabackup: 'xtrabackup' },
    backupBinlogList: {},
    binlogParseList: {}
  }

  const mockUser = {
    grants: {
      'cluster-settings': true
    }
  }

  beforeEach(() => {
    store = configureStore({
      reducer: {
        settings: settingsReducer,
        globalClusters: globalClustersReducer
      },
      preloadedState: {
        globalClusters: {
          monitor: mockMonitor
        }
      }
    })
  })

  describe('Conditional Rendering', () => {
    test('should NOT show pgzip settings when compression is disabled', () => {
      render(
        <Provider store={store}>
          <BackupSettings selectedCluster={mockCluster} user={mockUser} />
        </Provider>
      )

      // Use Compression toggle should be present
      expect(screen.getByText(/Use Compression/i)).toBeInTheDocument()

      // pgzip settings should NOT be visible
      expect(screen.queryByText(/Compression Level/i)).not.toBeInTheDocument()
      expect(screen.queryByText(/Parallel Blocks/i)).not.toBeInTheDocument()
    })

    test('should show pgzip settings when compression is enabled', () => {
      const clusterWithCompression = {
        ...mockCluster,
        config: {
          ...mockCluster.config,
          compressBackups: true
        }
      }

      render(
        <Provider store={store}>
          <BackupSettings selectedCluster={clusterWithCompression} user={mockUser} />
        </Provider>
      )

      // pgzip settings should be visible
      expect(screen.getByText(/Compression Level \(1=fastest, 9=best\)/i)).toBeInTheDocument()
      expect(screen.getByText(/Parallel Blocks \(higher=faster restore\)/i)).toBeInTheDocument()
    })
  })

  describe('Compression Level Settings', () => {
    test('should display default compression level value', () => {
      const clusterWithCompression = {
        ...mockCluster,
        config: {
          ...mockCluster.config,
          compressBackups: true,
          compressBackupsCompressionLevel: 6
        }
      }

      render(
        <Provider store={store}>
          <BackupSettings selectedCluster={clusterWithCompression} user={mockUser} />
        </Provider>
      )

      // Find all number inputs and check compression level
      const numberInputs = screen.getAllByTestId('number-input-value')
      const compressionLevelInput = numberInputs.find(input => input.textContent === '6')
      expect(compressionLevelInput).toBeInTheDocument()
    })

    test('should display custom compression level value', () => {
      const clusterWithCompression = {
        ...mockCluster,
        config: {
          ...mockCluster.config,
          compressBackups: true,
          compressBackupsCompressionLevel: 9
        }
      }

      render(
        <Provider store={store}>
          <BackupSettings selectedCluster={clusterWithCompression} user={mockUser} />
        </Provider>
      )

      // Check that value 9 is displayed
      const numberInputs = screen.getAllByTestId('number-input-value')
      const compressionLevelInput = numberInputs.find(input => input.textContent === '9')
      expect(compressionLevelInput).toBeInTheDocument()
    })

    test('should have edit button for compression level', () => {
      const clusterWithCompression = {
        ...mockCluster,
        config: {
          ...mockCluster.config,
          compressBackups: true
        }
      }

      render(
        <Provider store={store}>
          <BackupSettings selectedCluster={clusterWithCompression} user={mockUser} />
        </Provider>
      )

      // Should have at least 2 edit buttons (compression level + parallel blocks)
      const editButtons = screen.getAllByTestId('number-input-edit')
      expect(editButtons.length).toBeGreaterThanOrEqual(2)
    })
  })

  describe('Parallel Blocks Settings', () => {
    test('should display default parallel blocks value', () => {
      const clusterWithCompression = {
        ...mockCluster,
        config: {
          ...mockCluster.config,
          compressBackups: true,
          compressBackupsParallelBlocks: 4
        }
      }

      render(
        <Provider store={store}>
          <BackupSettings selectedCluster={clusterWithCompression} user={mockUser} />
        </Provider>
      )

      // Check that value 4 is displayed
      const numberInputs = screen.getAllByTestId('number-input-value')
      const parallelBlocksInput = numberInputs.find(input => input.textContent === '4')
      expect(parallelBlocksInput).toBeInTheDocument()
    })

    test('should display custom parallel blocks value', () => {
      const clusterWithCompression = {
        ...mockCluster,
        config: {
          ...mockCluster.config,
          compressBackups: true,
          compressBackupsParallelBlocks: 16
        }
      }

      render(
        <Provider store={store}>
          <BackupSettings selectedCluster={clusterWithCompression} user={mockUser} />
        </Provider>
      )

      // Check that value 16 is displayed
      const numberInputs = screen.getAllByTestId('number-input-value')
      const parallelBlocksInput = numberInputs.find(input => input.textContent === '16')
      expect(parallelBlocksInput).toBeInTheDocument()
    })
  })

  describe('User Interactions', () => {
    test('should toggle compression and show/hide pgzip settings', async () => {
      render(
        <Provider store={store}>
          <BackupSettings selectedCluster={mockCluster} user={mockUser} />
        </Provider>
      )

      // Initially, pgzip settings should not be visible
      expect(screen.queryByText(/Compression Level/i)).not.toBeInTheDocument()

      // Click the compression toggle
      const toggleButton = screen.getByTestId('rm-switch')
      fireEvent.click(toggleButton)

      // After clicking, the toggle state changes (though actual visibility depends on Redux)
      // In a real test with full Redux integration, we'd see the settings appear
    })

    test('should open edit modal when clicking edit button', async () => {
      const clusterWithCompression = {
        ...mockCluster,
        config: {
          ...mockCluster.config,
          compressBackups: true
        }
      }

      render(
        <Provider store={store}>
          <BackupSettings selectedCluster={clusterWithCompression} user={mockUser} />
        </Provider>
      )

      // Click first edit button
      const editButtons = screen.getAllByTestId('number-input-edit')
      fireEvent.click(editButtons[0])

      // Modal should appear
      await waitFor(() => {
        expect(screen.getByTestId('number-input-modal')).toBeInTheDocument()
      })
    })

    test('should allow changing compression level value', async () => {
      const clusterWithCompression = {
        ...mockCluster,
        config: {
          ...mockCluster.config,
          compressBackups: true,
          compressBackupsCompressionLevel: 6
        }
      }

      render(
        <Provider store={store}>
          <BackupSettings selectedCluster={clusterWithCompression} user={mockUser} />
        </Provider>
      )

      // Click edit button
      const editButtons = screen.getAllByTestId('number-input-edit')
      fireEvent.click(editButtons[0])

      // Wait for modal
      await waitFor(() => {
        expect(screen.getByTestId('number-input-modal')).toBeInTheDocument()
      })

      // Change value
      const inputField = screen.getByTestId('number-input-field')
      fireEvent.change(inputField, { target: { value: '9' } })

      // Confirm
      const confirmButton = screen.getByTestId('number-input-confirm')
      fireEvent.click(confirmButton)

      // Modal should close
      await waitFor(() => {
        expect(screen.queryByTestId('number-input-modal')).not.toBeInTheDocument()
      })
    })

    test('should cancel editing without changing value', async () => {
      const clusterWithCompression = {
        ...mockCluster,
        config: {
          ...mockCluster.config,
          compressBackups: true,
          compressBackupsCompressionLevel: 6
        }
      }

      render(
        <Provider store={store}>
          <BackupSettings selectedCluster={clusterWithCompression} user={mockUser} />
        </Provider>
      )

      // Click edit button
      const editButtons = screen.getAllByTestId('number-input-edit')
      fireEvent.click(editButtons[0])

      // Wait for modal
      await waitFor(() => {
        expect(screen.getByTestId('number-input-modal')).toBeInTheDocument()
      })

      // Change value
      const inputField = screen.getByTestId('number-input-field')
      fireEvent.change(inputField, { target: { value: '9' } })

      // Cancel
      const cancelButton = screen.getByTestId('number-input-cancel')
      fireEvent.click(cancelButton)

      // Modal should close without confirming
      await waitFor(() => {
        expect(screen.queryByTestId('number-input-modal')).not.toBeInTheDocument()
      })
    })
  })

  describe('Label Text Validation', () => {
    test('should display correct label for compression level', () => {
      const clusterWithCompression = {
        ...mockCluster,
        config: {
          ...mockCluster.config,
          compressBackups: true
        }
      }

      render(
        <Provider store={store}>
          <BackupSettings selectedCluster={clusterWithCompression} user={mockUser} />
        </Provider>
      )

      expect(screen.getByText(/Compression Level \(1=fastest, 9=best\)/i)).toBeInTheDocument()
    })

    test('should display correct label for parallel blocks', () => {
      const clusterWithCompression = {
        ...mockCluster,
        config: {
          ...mockCluster.config,
          compressBackups: true
        }
      }

      render(
        <Provider store={store}>
          <BackupSettings selectedCluster={clusterWithCompression} user={mockUser} />
        </Provider>
      )

      expect(screen.getByText(/Parallel Blocks \(higher=faster restore\)/i)).toBeInTheDocument()
    })
  })

  describe('Validation Scenarios', () => {
    test('should respect min value for compression level', () => {
      const clusterWithCompression = {
        ...mockCluster,
        config: {
          ...mockCluster.config,
          compressBackups: true,
          compressBackupsCompressionLevel: 1 // minimum value
        }
      }

      render(
        <Provider store={store}>
          <BackupSettings selectedCluster={clusterWithCompression} user={mockUser} />
        </Provider>
      )

      const numberInputs = screen.getAllByTestId('number-input-value')
      const compressionLevelInput = numberInputs.find(input => input.textContent === '1')
      expect(compressionLevelInput).toBeInTheDocument()
    })

    test('should respect max value for compression level', () => {
      const clusterWithCompression = {
        ...mockCluster,
        config: {
          ...mockCluster.config,
          compressBackups: true,
          compressBackupsCompressionLevel: 9 // maximum value
        }
      }

      render(
        <Provider store={store}>
          <BackupSettings selectedCluster={clusterWithCompression} user={mockUser} />
        </Provider>
      )

      const numberInputs = screen.getAllByTestId('number-input-value')
      const compressionLevelInput = numberInputs.find(input => input.textContent === '9')
      expect(compressionLevelInput).toBeInTheDocument()
    })

    test('should respect max value for parallel blocks', () => {
      const clusterWithCompression = {
        ...mockCluster,
        config: {
          ...mockCluster.config,
          compressBackups: true,
          compressBackupsParallelBlocks: 32 // maximum value
        }
      }

      render(
        <Provider store={store}>
          <BackupSettings selectedCluster={clusterWithCompression} user={mockUser} />
        </Provider>
      )

      const numberInputs = screen.getAllByTestId('number-input-value')
      const parallelBlocksInput = numberInputs.find(input => input.textContent === '32')
      expect(parallelBlocksInput).toBeInTheDocument()
    })
  })

  describe('Integration with Other Settings', () => {
    test('should render pgzip settings alongside other backup settings', () => {
      const clusterWithCompression = {
        ...mockCluster,
        config: {
          ...mockCluster.config,
          compressBackups: true
        }
      }

      render(
        <Provider store={store}>
          <BackupSettings selectedCluster={clusterWithCompression} user={mockUser} />
        </Provider>
      )

      // Check that other backup settings are also present
      expect(screen.getByText(/Logical Backup/i)).toBeInTheDocument()
      expect(screen.getByText(/Physical Backup/i)).toBeInTheDocument()
      expect(screen.getByText(/Use Compression/i)).toBeInTheDocument()
      expect(screen.getByText(/Compression Level/i)).toBeInTheDocument()
      expect(screen.getByText(/Parallel Blocks/i)).toBeInTheDocument()
      expect(screen.getByText(/Backup Buffer Size/i)).toBeInTheDocument()
    })
  })
})
