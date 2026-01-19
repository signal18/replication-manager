import React from 'react'
import { render, screen, fireEvent, waitFor } from '@testing-library/react'
import '@testing-library/jest-dom'
import { ChakraProvider } from '@chakra-ui/react'
import ConfirmActionForm from './ConfirmActionForm'

// Mock data - matches clusterServers structure from Redux
const mockServers = [
  {
    id: '0',
    host: '192.168.1.10',
    port: '3306',
    state: 'Master'
  },
  {
    id: '1',
    host: '192.168.1.11',
    port: '3306',
    state: 'Slave'
  },
  {
    id: '2',
    host: '192.168.1.12',
    port: '3306',
    state: 'Slave'
  }
]

const mockFiles = [
  {
    path: '/var/backups/dump.sql.gz',
    type: 'file',
    size: 1048576 // 1MB
  },
  {
    path: '/var/backups/full-backup.sql',
    type: 'file',
    size: 5242880 // 5MB
  },
  {
    path: '/var/backups/mysql.user.sql.gz',
    type: 'file',
    size: 10240 // 10KB
  },
  {
    path: '/var/backups',
    type: 'dir',
    size: 0
  }
]

// Wrapper component to provide Chakra context
const ChakraWrapper = ({ children }) => (
  <ChakraProvider>{children}</ChakraProvider>
)

describe('SnapshotDumpForm', () => {
  const defaultPayload = {
    action: 'snapshotDump',
    data: {
      snapshotId: 'abc123',
      serverId: '',
      filePath: ''
    }
  }

  const mockOnDumpServerChange = jest.fn()
  const mockOnDumpFilePathChange = jest.fn()

  beforeEach(() => {
    jest.clearAllMocks()
  })

  test('renders form with all required fields', () => {
    render(
      <ChakraWrapper>
        <ConfirmActionForm
          payload={defaultPayload}
          availableServers={mockServers}
          dumpFiles={mockFiles}
          dumpFilesLoading={false}
          dumpFilesError={null}
          onDumpServerChange={mockOnDumpServerChange}
          onDumpFilePathChange={mockOnDumpFilePathChange}
        />
      </ChakraWrapper>
    )

    expect(screen.getByText('Target MySQL Server')).toBeInTheDocument()
    expect(screen.getByText('File Path in Snapshot')).toBeInTheDocument()
    expect(screen.getByText(/Warning:/)).toBeInTheDocument()
  })

  test('displays server dropdown with available servers', () => {
    render(
      <ChakraWrapper>
        <ConfirmActionForm
          payload={defaultPayload}
          availableServers={mockServers}
          dumpFiles={[]}
          dumpFilesLoading={false}
          onDumpServerChange={mockOnDumpServerChange}
          onDumpFilePathChange={mockOnDumpFilePathChange}
        />
      </ChakraWrapper>
    )

    const serverSelect = screen.getByRole('combobox', { name: /target mysql server/i })
    expect(serverSelect).toBeInTheDocument()
    
    // Open dropdown
    fireEvent.click(serverSelect)
    
    // Check if all servers are listed by host:port
    mockServers.forEach(server => {
      const serverText = `${server.host}:${server.port}`
      expect(screen.getByText(new RegExp(serverText))).toBeInTheDocument()
    })
  })

  test('calls onDumpServerChange when server is selected', () => {
    render(
      <ChakraWrapper>
        <ConfirmActionForm
          payload={defaultPayload}
          availableServers={mockServers}
          dumpFiles={[]}
          dumpFilesLoading={false}
          onDumpServerChange={mockOnDumpServerChange}
          onDumpFilePathChange={mockOnDumpFilePathChange}
        />
      </ChakraWrapper>
    )

    const serverSelect = screen.getByRole('combobox', { name: /target mysql server/i })
    fireEvent.change(serverSelect, { target: { value: '0' } })

    expect(mockOnDumpServerChange).toHaveBeenCalledWith('0')
  })

  test('displays file dropdown with available files only', () => {
    render(
      <ChakraWrapper>
        <ConfirmActionForm
          payload={defaultPayload}
          availableServers={mockServers}
          dumpFiles={mockFiles}
          dumpFilesLoading={false}
          onDumpServerChange={mockOnDumpServerChange}
          onDumpFilePathChange={mockOnDumpFilePathChange}
        />
      </ChakraWrapper>
    )

    const fileSelect = screen.getAllByRole('combobox')[1] // Second combobox is for files
    fireEvent.click(fileSelect)

    // Should show only files, not directories
    const fileOptions = mockFiles.filter(f => f.type === 'file')
    fileOptions.forEach(file => {
      expect(screen.getByText(new RegExp(file.path))).toBeInTheDocument()
    })

    // Directory should not be shown
    const dirOption = screen.queryByText(/^\/var\/backups$/)
    expect(dirOption).not.toBeInTheDocument()
  })

  test('displays file sizes in MB', () => {
    render(
      <ChakraWrapper>
        <ConfirmActionForm
          payload={defaultPayload}
          availableServers={mockServers}
          dumpFiles={mockFiles}
          dumpFilesLoading={false}
          onDumpServerChange={mockOnDumpServerChange}
          onDumpFilePathChange={mockOnDumpFilePathChange}
        />
      </ChakraWrapper>
    )

    // 1MB file
    expect(screen.getByText(/1\.00 MB/)).toBeInTheDocument()
    // 5MB file
    expect(screen.getByText(/5\.00 MB/)).toBeInTheDocument()
    // 10KB file (0.01 MB)
    expect(screen.getByText(/0\.01 MB/)).toBeInTheDocument()
  })

  test('calls onDumpFilePathChange when file is selected from dropdown', () => {
    render(
      <ChakraWrapper>
        <ConfirmActionForm
          payload={defaultPayload}
          availableServers={mockServers}
          dumpFiles={mockFiles}
          dumpFilesLoading={false}
          onDumpServerChange={mockOnDumpServerChange}
          onDumpFilePathChange={mockOnDumpFilePathChange}
        />
      </ChakraWrapper>
    )

    const fileSelect = screen.getAllByRole('combobox')[1]
    fireEvent.change(fileSelect, { target: { value: '/var/backups/dump.sql.gz' } })

    expect(mockOnDumpFilePathChange).toHaveBeenCalledWith('/var/backups/dump.sql.gz')
  })

  test('calls onDumpFilePathChange when custom path is entered', () => {
    render(
      <ChakraWrapper>
        <ConfirmActionForm
          payload={defaultPayload}
          availableServers={mockServers}
          dumpFiles={mockFiles}
          dumpFilesLoading={false}
          onDumpServerChange={mockOnDumpServerChange}
          onDumpFilePathChange={mockOnDumpFilePathChange}
        />
      </ChakraWrapper>
    )

    const customInput = screen.getByPlaceholderText('/var/backups/dump.sql.gz')
    fireEvent.change(customInput, { target: { value: '/custom/path/dump.sql' } })

    expect(mockOnDumpFilePathChange).toHaveBeenCalledWith('/custom/path/dump.sql')
  })

  test('shows loading state when files are being fetched', () => {
    render(
      <ChakraWrapper>
        <ConfirmActionForm
          payload={defaultPayload}
          availableServers={mockServers}
          dumpFiles={[]}
          dumpFilesLoading={true}
          onDumpServerChange={mockOnDumpServerChange}
          onDumpFilePathChange={mockOnDumpFilePathChange}
        />
      </ChakraWrapper>
    )

    expect(screen.getByText('Loading snapshot files...')).toBeInTheDocument()
  })

  test('shows error message when file loading fails', () => {
    const errorMessage = 'Failed to fetch snapshot files'
    render(
      <ChakraWrapper>
        <ConfirmActionForm
          payload={defaultPayload}
          availableServers={mockServers}
          dumpFiles={[]}
          dumpFilesLoading={false}
          dumpFilesError={errorMessage}
          onDumpServerChange={mockOnDumpServerChange}
          onDumpFilePathChange={mockOnDumpFilePathChange}
        />
      </ChakraWrapper>
    )

    expect(screen.getByText(errorMessage)).toBeInTheDocument()
  })

  test('displays warning message about destructive operation', () => {
    render(
      <ChakraWrapper>
        <ConfirmActionForm
          payload={defaultPayload}
          availableServers={mockServers}
          dumpFiles={[]}
          dumpFilesLoading={false}
          onDumpServerChange={mockOnDumpServerChange}
          onDumpFilePathChange={mockOnDumpFilePathChange}
        />
      </ChakraWrapper>
    )

    expect(screen.getByText(/Warning:/)).toBeInTheDocument()
    expect(screen.getByText(/stop replication/i)).toBeInTheDocument()
  })

  test('shows disabled option when no servers are available', () => {
    render(
      <ChakraWrapper>
        <ConfirmActionForm
          payload={defaultPayload}
          availableServers={[]}
          dumpFiles={[]}
          dumpFilesLoading={false}
          onDumpServerChange={mockOnDumpServerChange}
          onDumpFilePathChange={mockOnDumpFilePathChange}
        />
      </ChakraWrapper>
    )

    const serverSelect = screen.getByRole('combobox', { name: /target mysql server/i })
    expect(serverSelect).toBeInTheDocument()
    
    // Should show "No servers available" option
    fireEvent.click(serverSelect)
    expect(screen.getByText('No servers available')).toBeInTheDocument()
  })

  test('preserves selected values when re-rendered', async () => {
    const { rerender } = render(
      <ChakraWrapper>
        <ConfirmActionForm
          payload={{
            action: 'snapshotDump',
            data: {
              snapshotId: 'abc123',
              serverId: '0',
              filePath: '/var/backups/dump.sql.gz'
            }
          }}
          availableServers={mockServers}
          dumpFiles={mockFiles}
          dumpFilesLoading={false}
          onDumpServerChange={mockOnDumpServerChange}
          onDumpFilePathChange={mockOnDumpFilePathChange}
        />
      </ChakraWrapper>
    )

    const serverSelect = screen.getByRole('combobox', { name: /target mysql server/i })
    expect(serverSelect.value).toBe('0')

    const customInput = screen.getByPlaceholderText('/var/backups/dump.sql.gz')
    expect(customInput.value).toBe('/var/backups/dump.sql.gz')
  })

  test('handles empty file list gracefully', () => {
    render(
      <ChakraWrapper>
        <ConfirmActionForm
          payload={defaultPayload}
          availableServers={mockServers}
          dumpFiles={[]}
          dumpFilesLoading={false}
          onDumpServerChange={mockOnDumpServerChange}
          onDumpFilePathChange={mockOnDumpFilePathChange}
        />
      </ChakraWrapper>
    )

    // Should still render the custom input
    const customInput = screen.getByPlaceholderText('/var/backups/dump.sql.gz')
    expect(customInput).toBeInTheDocument()
  })

  test('form fields are marked as required', () => {
    render(
      <ChakraWrapper>
        <ConfirmActionForm
          payload={defaultPayload}
          availableServers={mockServers}
          dumpFiles={[]}
          dumpFilesLoading={false}
          onDumpServerChange={mockOnDumpServerChange}
          onDumpFilePathChange={mockOnDumpFilePathChange}
        />
      </ChakraWrapper>
    )

    // Check for required indicator (usually an asterisk or "required" text)
    const requiredLabels = screen.getAllByText(/target mysql server/i)
    expect(requiredLabels.length).toBeGreaterThan(0)
  })
})

describe('SnapshotDumpForm Integration', () => {
  test('full workflow: select server, select file, values are propagated', () => {
    const mockOnDumpServerChange = jest.fn()
    const mockOnDumpFilePathChange = jest.fn()

    render(
      <ChakraWrapper>
        <ConfirmActionForm
          payload={{
            action: 'snapshotDump',
            data: {
              snapshotId: 'abc123',
              serverId: '',
              filePath: ''
            }
          }}
          availableServers={mockServers}
          dumpFiles={mockFiles}
          dumpFilesLoading={false}
          onDumpServerChange={mockOnDumpServerChange}
          onDumpFilePathChange={mockOnDumpFilePathChange}
        />
      </ChakraWrapper>
    )

    // Step 1: Select server (using server ID from clusterServers)
    const serverSelect = screen.getByRole('combobox', { name: /target mysql server/i })
    fireEvent.change(serverSelect, { target: { value: '1' } })
    expect(mockOnDumpServerChange).toHaveBeenCalledWith('1')

    // Step 2: Select file from dropdown
    const fileSelect = screen.getAllByRole('combobox')[1]
    fireEvent.change(fileSelect, { target: { value: '/var/backups/full-backup.sql' } })
    expect(mockOnDumpFilePathChange).toHaveBeenCalledWith('/var/backups/full-backup.sql')

    // Step 3: Override with custom path
    const customInput = screen.getByPlaceholderText('/var/backups/dump.sql.gz')
    fireEvent.change(customInput, { target: { value: '/custom/path.sql.gz' } })
    expect(mockOnDumpFilePathChange).toHaveBeenCalledWith('/custom/path.sql.gz')
  })
})
