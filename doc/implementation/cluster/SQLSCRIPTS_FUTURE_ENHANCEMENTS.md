# SQL Scripts Feature - Improvements Plan

**Date:** January 15, 2026
**Status:** 🚧 IN PROGRESS

## Improvements Requested

### 1. Multi-Server Target Selection (Checkboxes)
Instead of dropdown, allow users to select which servers to target:
- ☐ Master (Primary)
- ☐ Server1 (192.168.1.101:3306)
- ☐ Server2 (192.168.1.102:3306)
- ☐ Server3 (192.168.1.103:3306)

**Benefits:**
- Execute on multiple servers at once
- More flexible targeting
- Better for cluster-wide operations
- Clear visual indication of targets

### 2. Optional Custom Credentials
Allow users to specify different credentials for script execution:
- Custom User (optional)
- Custom Password (optional - encrypted)

**Benefits:**
- Run scripts with specific user privileges
- Test scripts with different permission levels
- Security: passwords encrypted using existing functions

## Implementation Plan

### Phase 1: Backend Changes

#### A. Update SQLScriptJob Structure ✅
```go
type SQLScriptJob struct {
    // ... existing fields ...
    TargetServers    []string  `json:"targetServers"` // NEW: Array of server IDs
    TargetServer     string    `json:"targetServer"`  // DEPRECATED: kept for compatibility
    CustomUser       string    `json:"customUser,omitempty"` // NEW
    CustomPassword   string    `json:"customPassword,omitempty"` // NEW: encrypted
}
```

#### B. Add Encryption/Decryption Support
Use existing functions:
- `conf.GetEncryptedString(password)` - Encrypt before saving
- `conf.GetDecryptedValue(key)` - Decrypt before use

#### C. Update Execution Logic
- Loop through TargetServers array
- Execute on each server sequentially or in parallel
- Use CustomUser/CustomPassword if provided
- Decrypt password before connecting
- Return aggregated results

#### D. Add Server List API (Already Exists ✅)
Endpoint: `GET /api/clusters/{clusterName}/topology/servers`
Returns list of servers with ID, URL, role, state

### Phase 2: Frontend Changes

#### A. Replace Dropdown with Checkboxes
**Quick Execute Form:**
```jsx
<VStack align="stretch">
  <Text fontWeight="bold">Target Servers:</Text>
  <Checkbox value="master" defaultChecked>
    Master (Primary) - 192.168.1.100:3306
  </Checkbox>
  {servers.map(srv => (
    <Checkbox key={srv.id} value={srv.id}>
      {srv.name} ({srv.state}) - {srv.url}
    </Checkbox>
  ))}
</VStack>
```

**Modals:** Similar checkbox implementation

#### B. Add Optional Credentials Section
```jsx
<Accordion allowToggle>
  <AccordionItem>
    <AccordionButton>
      <Box flex="1" textAlign="left">
        Custom Credentials (Optional)
      </Box>
      <AccordionIcon />
    </AccordionButton>
    <AccordionPanel>
      <FormControl>
        <FormLabel>Username</FormLabel>
        <Input placeholder="Leave empty to use default" />
      </FormControl>
      <FormControl>
        <FormLabel>Password</FormLabel>
        <Input type="password" placeholder="Leave empty to use default" />
      </FormControl>
      <Alert status="info">
        Password will be encrypted before storage
      </Alert>
    </AccordionPanel>
  </AccordionItem>
</Accordion>
```

### Phase 3: API Updates

#### A. Update Execute Endpoint
```go
type ExecuteScriptRequest struct {
    ScriptPath     string   `json:"scriptPath"`
    ScriptContent  string   `json:"scriptContent"`
    TargetDatabase string   `json:"targetDatabase"`
    TargetServers  []string `json:"targetServers"` // NEW
    CustomUser     string   `json:"customUser"`    // NEW
    CustomPassword string   `json:"customPassword"` // NEW (client sends plain, server encrypts)
    Timeout        int      `json:"timeout"`
}
```

#### B. Update Job Save Endpoint
Same structure - encrypt password on server before saving

### Phase 4: Execution Logic

#### Server Selection Logic
```go
func (cluster *Cluster) executeOnTargetServers(job *SQLScriptJob) []Result {
    results := make([]Result, 0)
    
    // Get target servers
    servers := cluster.getServersByTargets(job.TargetServers)
    
    for _, server := range servers {
        // Use custom credentials if provided
        user := job.CustomUser
        pass := job.CustomPassword
        
        if user == "" {
            user = server.User
            pass = server.Pass
        } else {
            // Decrypt custom password
            pass = cluster.Conf.GetDecryptedValue(pass)
        }
        
        // Execute on server
        result := cluster.executeScriptOnServer(server, job, user, pass)
        results = append(results, result)
    }
    
    return results
}
```

## UI/UX Improvements

### Before (Current)
```
┌─────────────────────────────────────┐
│ Target Server: Master (Primary)     │
└─────────────────────────────────────┘
```

### After (Improved)
```
┌─────────────────────────────────────┐
│ Target Servers:                     │
│ ☑ Master (Primary) - 192.168.1.100 │
│ ☐ Slave1 (Running) - 192.168.1.101 │
│ ☐ Slave2 (Running) - 192.168.1.102 │
│                                     │
│ ▼ Custom Credentials (Optional)    │
│   └─ [collapsed by default]        │
└─────────────────────────────────────┘
```

## Security Considerations

1. **Password Encryption:**
   - Client sends plain password (over HTTPS)
   - Server encrypts using `GetEncryptedString()`
   - Stored encrypted in job definition
   - Decrypted only during execution

2. **Password Display:**
   - Never show password in UI after save
   - Use `*****` masking in job list
   - Don't return password in API responses

3. **Validation:**
   - Validate server IDs exist before execution
   - Validate user has permission to execute on servers
   - Test credentials before saving job

## Testing Checklist

- [ ] Multi-server selection works
- [ ] Master checkbox pre-checked by default
- [ ] Server list loads correctly
- [ ] Checkbox state persists
- [ ] Custom credentials optional
- [ ] Password encryption works
- [ ] Password decryption works
- [ ] Scripts execute on selected servers
- [ ] Results aggregated correctly
- [ ] Error handling for failed servers
- [ ] Job save with encrypted password
- [ ] Job load doesn't expose password
- [ ] Backward compatibility maintained

## API Changes Summary

### New Fields (Backward Compatible)
```json
{
  "targetServers": ["master", "server-id-1"],  // NEW
  "customUser": "scriptuser",                  // NEW
  "customPassword": "hash_encrypted_value"     // NEW
}
```

### Existing Field (Deprecated but Supported)
```json
{
  "targetServer": "master"  // DEPRECATED: converted to targetServers array
}
```

## Implementation Order

1. ✅ Update backend struct (SQLScriptJob)
2. ⏳ Update execution logic for multi-server
3. ⏳ Add encryption/decryption for custom password
4. ⏳ Update API to handle new fields
5. ⏳ Add server list API call in frontend
6. ⏳ Replace dropdown with checkboxes in UI
7. ⏳ Add optional credentials section in UI
8. ⏳ Update Redux actions/thunks
9. ⏳ Test end-to-end functionality
10. ⏳ Update documentation

## Status

**Current:** Backend struct updated  
**Next:** Implement execution logic  
**ETA:** TBD

---

**Note:** This is a significant enhancement that improves flexibility and usability while maintaining backward compatibility.
