# ServerMenu Component Documentation

## Overview
The `ServerMenu` component provides a context menu for database server operations in the Replication Manager dashboard. It offers various database management actions organized into logical groups based on user permissions.

## Architecture

### Configuration-Based Database Management
The component uses a configuration file-based approach for database startup and management:

- **01_preserved.cnf**: User-accepted differences (overrides with highest precedence)
- **02_delta.cnf**: Detected differences between deployed and config
- **03_agreed.cnf**: Variables that should match between systems

This approach simplifies the database start mechanism by moving configuration options from multiple UI buttons to backend configuration files.

## Component Props

| Prop | Type | Required | Description |
|------|------|----------|-------------|
| `clusterName` | string | Yes | Name of the database cluster |
| `clusterMasterId` | string | Yes | ID of the cluster master server |
| `backupPhysicalType` | string | Yes | Type of physical backup (e.g., 'xtrabackup') |
| `backupLogicalType` | string | Yes | Type of logical backup (e.g., 'mysqldump') |
| `orchestrator` | string | Yes | Orchestrator type (e.g., 'opensvc') |
| `row` | object | Yes | Server data object containing id, host, port, isSlave, prefered, ignored |
| `user` | object | Yes | User object with grants/permissions |
| `isDesktop` | boolean | Yes | Flag for desktop vs mobile layout |
| `from` | string | No | Origin of menu ('tableView' or other), defaults to 'tableView' |
| `openCompareModal` | function | Yes | Callback to open server comparison modal |
| `colorScheme` | string | No | Color scheme for the menu |
| `className` | string | No | Additional CSS classes |
| `showCompareWithOption` | boolean | No | Show/hide compare option, defaults to true |
| `showTerminal` | boolean | No | Show/hide terminal options, defaults to false |

## Menu Structure

### 1. Compare With (Optional)
- **Permission**: Always visible if `showCompareWithOption` is true
- **Action**: Opens comparison modal for server configuration/status comparison

### 2. Maintenance Mode
- **Permission**: Always visible
- **Action**: Toggles maintenance mode for the server
- **Purpose**: Prevents automatic failover actions on this server

### 3. Web Terminal (Conditional)
- **Permission**: `user.grants['terminal-db']` and `showTerminal` is true
- **Sub-options**:
  - MySQL Terminal: Direct MySQL CLI access
  - MyTop Terminal: Real-time MySQL monitoring
  - Shell Terminal: System shell access (requires `user.grants['terminal-global']`)

### 4. Promote To Leader (Conditional)
- **Permission**: `user.grants['cluster-switchover']` and server is a slave
- **Action**: Promotes slave to master/leader role
- **Use Case**: Manual switchover scenarios

### 5. Failover Candidate
- **Sub-options**:
  - **Set as Preferred**: Mark server as preferred for failover (when not already preferred/ignored)
    - Permission: `user.grants['cluster-failover']`
  - **Set as Ignored**: Mark server to be ignored for failover (when not already preferred/ignored)
    - Permission: `user.grants['cluster-failover']`
  - **Set as Unrated**: Remove preferred/ignored status
    - Permission: `user.grants['cluster-failover']`

### 6. Backup
- **Master Server Options** (when `clusterMasterId === row.id`):
  - **Physical Backup**: Create physical backup using configured method
    - Permission: `user.grants['db-backup']`
  - **Logical Backup**: Create logical backup (mysqldump, mydumper)
    - Permission: `user.grants['db-backup']`

- **Slave Server Options** (when server is not master):
  - **Reseed Logical From Backup**: Restore from logical backup
    - Permission: `user.grants['db-restore']`
  - **Reseed Logical From Master**: Clone from master using logical method
    - Permission: `user.grants['db-restore']`
  - **Reseed Physical From Backup**: Restore from physical backup
    - Permission: `user.grants['db-restore']`

- **Common Options**:
  - **Flush Logs**: Rotate binary/relay logs
    - Permission: `user.grants['db-backup']`
  - **Run Remote Jobs**: Execute scheduled jobs container tasks
    - Permission: Always visible

### 7. Provision
- **Jobs Upgrade**: Update jobs container to latest version
  - Permission: `user.grants['db-maintenance']`
- **Stop Database**: Stop database server
  - Permission: `user.grants['db-stop']`
- **Start Database**: Start database server
  - Permission: `user.grants['db-start']`
  - **Note**: Uses configuration-based startup (01_preserved.cnf, 02_delta.cnf, 03_agreed.cnf)
- **Restart Jobs Container**: Restart jobs container (OpenSVC only)
  - Permission: `user.grants['db-start']` and `orchestrator === 'opensvc'`
- **Provision Database**: Provision new database instance
  - Permission: `user.grants['prov-db-provision']`
- **Unprovision Database**: Remove database instance
  - Permission: `user.grants['prov-db-unprovision']`
- **Refresh Variables and Generate Config**: Regenerate configuration files
  - Permission: `user.grants['db-config-flag']`
  - **Purpose**: Refreshes variables and regenerates 02_delta.cnf based on differences
- **Remove Monitor**: Remove server from monitoring
  - Permission: Always visible

### 8. DB Utils
- **Optimize**: Run OPTIMIZE TABLE on all tables
  - Permission: `user.grants['db-optimize']`
- **Skip 1 Replication Event**: Skip one replication error
  - Permission: `user.grants['db-replication']`
- **Toggle InnoDB Monitor**: Enable/disable InnoDB monitoring
  - Permission: `user.grants['db-logs']`
- **Toggle Slow Query Capture**: Enable/disable slow query logging
  - Permission: `user.grants['db-capture']`
- **Start Slave**: Start replication on slave
  - Permission: `user.grants['db-replication']`
- **Stop Slave**: Stop replication on slave
  - Permission: `user.grants['db-replication']`
- **Reset Master**: Reset binary log position (dangerous on master)
  - Permission: `user.grants['db-replication']`
- **Reset Slave**: Reset replication state (breaks replication)
  - Permission: `user.grants['db-replication']`
- **Toggle Readonly**: Toggle read-only mode
  - Permission: `user.grants['db-readonly']`

## Configuration File Integration

### How It Works
1. **Database Start**: When "Start Database" is clicked, the backend reads configuration from:
   - Config template (from replication-manager config)
   - `01_preserved.cnf` (user overrides - highest priority)
   - `02_delta.cnf` (detected differences)
   - `03_agreed.cnf` (agreed differences)

2. **Generate Config**: The "Refresh Variables and Generate Config" action:
   - Fetches current database variables
   - Compares with expected configuration
   - Updates `02_delta.cnf` with detected differences
   - Allows user to review and preserve specific differences in `01_preserved.cnf`

### Benefits
- **Simplified UI**: No multiple start options with different configurations
- **Persistent Configuration**: User preferences survive restarts
- **Audit Trail**: Configuration changes are tracked in files
- **Conflict Resolution**: Clear precedence order for configuration values

## State Management

### Local State
- `isConfirmModalOpen`: Controls confirmation modal visibility
- `confirmTitle`: Dynamic title for confirmation modal
- `confirmHandler`: Function to execute on confirmation
- `serverName`: Formatted server name for display

### Redux Actions
All actions are dispatched to the Redux store and handled by `clusterSlice.js`:
- Server control: `startDatabase`, `stopDatabase`, `restartDatabase`
- Replication: `startSlave`, `stopSlave`, `resetMaster`, `resetSlaveAll`
- Backup/Restore: `logicalBackup`, `physicalBackupMaster`, `reseedLogicalFromBackup`, etc.
- Configuration: `generateConfig` (from `configSlice.js`)
- Maintenance: `setMaintenanceMode`, `optimizeServer`, `jobsUpgrade`
- Failover: `promoteToLeader`, `setAsPreferred`, `setAsIgnored`, `setAsUnrated`

## Known Issues

### 1. Incorrect Confirmation Titles
**Location**: Lines 149, 158
**Issue**: Both "Set as Preferred" and "Set as Ignored" options show the same confirmation message:
```javascript
setConfirmTitle(`Confirm set as unrated for ${serverName}?`)
```
**Expected**: Should say "set as preferred" and "set as ignored" respectively.

**Fix Required**: Update confirmation titles to match the actual action.

### 2. Missing Import Usage Warning
**Location**: Line 35
**Issue**: `preserveConfigPath` is imported but never used in the component.
```javascript
import { generateConfig, preserveConfigPath } from '../../../../redux/configSlice'
```
**Impact**: Unused import (minor code cleanliness issue)

**Recommendation**: Either implement the feature or remove the import.

## Usage Example

```jsx
<ServerMenu
  clusterName="prod-cluster"
  clusterMasterId="server1"
  backupPhysicalType="xtrabackup"
  backupLogicalType="mysqldump"
  orchestrator="opensvc"
  row={{
    id: 'server1',
    host: '192.168.1.100',
    port: 3306,
    isSlave: false,
    prefered: false,
    ignored: false
  }}
  user={{
    grants: {
      'terminal-db': true,
      'db-start': true,
      'db-stop': true,
      'db-backup': true,
      'cluster-switchover': true
    }
  }}
  isDesktop={true}
  from="tableView"
  openCompareModal={(row) => console.log('Compare:', row)}
  colorScheme="dark"
  showCompareWithOption={true}
  showTerminal={true}
/>
```

## Security Considerations

1. **Permission-Based Rendering**: All dangerous operations are protected by user grants
2. **Confirmation Modals**: Critical operations require user confirmation
3. **Audit Trail**: All actions are logged through Redux state management
4. **Server Identification**: Actions always include clusterName and serverId for proper targeting

## Future Enhancements

1. **Bulk Operations**: Support for selecting multiple servers
2. **Operation History**: Show recent operations per server
3. **Custom Confirmation Messages**: More detailed warnings for dangerous operations
4. **Keyboard Shortcuts**: Quick access to common operations
5. **Operation Status**: Real-time feedback on long-running operations

## Related Components

- `MenuOptions`: Base menu component providing dropdown functionality
- `ConfirmModal`: Confirmation dialog for destructive operations
- `clusterSlice.js`: Redux slice managing cluster state and actions
- `configSlice.js`: Redux slice managing configuration operations
- `clusterService.js`: API service for backend communication

## Testing Recommendations

1. **Permission Testing**: Verify menu options appear/hide based on user grants
2. **Confirmation Flow**: Test all confirmation modals trigger correctly
3. **Edge Cases**: Test with missing props, null values, offline servers
4. **Orchestrator Variants**: Test with different orchestrator types
5. **Mobile/Desktop**: Verify menu placement and submenu behavior
6. **Configuration Generation**: Test the generate config flow end-to-end
