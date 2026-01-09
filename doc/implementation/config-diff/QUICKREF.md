# Config Diff Indicator - Quick Reference

## Visual Indicators

### Table View
| State | Indicator | Meaning | Action |
|-------|-----------|---------|--------|
| Synced | 🟢 Green checkmark | Config matches deployed | None needed |
| Diff | 🟠 Orange alert icon | Differences exist | **Click to view Variables tab** |

### Grid View
| State | Indicator | Meaning | Action |
|-------|-----------|---------|--------|
| Synced | (none) | Config matches - clean UI | None needed |
| Diff | 🟠 "Config Diff" tag | Differences exist | **Click to view Variables tab** |

## User Workflow

1. **Detect**: See orange indicator in Dashboard
2. **Click**: Click the icon/tag to navigate
3. **Investigate**: Variables tab opens automatically
4. **Resolve**: Preserve, accept, or fix the difference

## Quick Actions

### Check for Config Drift
```bash
# View all servers with diff
curl -X GET http://localhost:10001/api/clusters/default/servers \
  | jq '.[] | select(.hasConfigDiff == true) | {host, port, hasConfigDiff}'
```

### Run Tests
```bash
# Go tests
go test -v ./cluster -run TestHasConfigDiff

# All tests
./doc/implementation/config-diff/test_config_diff.sh
```

### Debug
```bash
# Check monitoring logs
grep "HasConfigDiff" /var/log/replication-manager.log

# Compare configs
diff -u /path/to/generated.cnf /path/to/deployed.cnf
```

## API Response
```json
{
  "hasConfigDiff": true  // or false
}
```

## Common Scenarios

| Scenario | hasConfigDiff | Action |
|----------|---------------|--------|
| Fresh install | `false` | ✅ Normal |
| Manual config edit | `true` | Review & preserve/accept |
| Preserved variable | Depends | Check if intentional |
| External tool change | `true` | Review & resolve |

## Troubleshooting

| Issue | Check | Solution |
|-------|-------|----------|
| Always true | Config format | Normalize whitespace/comments |
| Never true | Monitoring | Verify cycle running |
| Not showing | Frontend | Check Redux state |
| Wrong tooltip | ChakraUI | Verify Provider wrapper |

## Test Files

- `cluster/srv_config_diff_test.go` - Backend tests
- `DBServers/__tests__/DBServers.configdiff.test.jsx` - Table tests (with clickability)
- `DBServerGrid/__tests__/DBServerGrid.configdiff.test.jsx` - Grid tests (with clickability)
- `ClusterDB/__tests__/ClusterDB.autoopen.test.jsx` - Auto-open Variables tab tests

## Related Commands

```bash
# View variables with diff
replication-manager-cli api --cluster default \
  --command variables --server db1 --diff

# Preserve a variable
curl -X POST http://localhost:10001/api/clusters/default/servers/db1/variables-preserve \
  -d '{"variableName": "max_connections"}'

# Accept config value
curl -X POST http://localhost:10001/api/clusters/default/servers/db1/variables-accept \
  -d '{"variableName": "max_connections"}'
```

## Performance

- **Backend**: O(1) best case, O(n) worst case
- **Frontend**: No additional API calls
- **Monitoring**: Minimal overhead (~0.1ms)

## Key Files

| File | Purpose |
|------|---------|
| `cluster/srv.go` | HasConfigDiff field |
| `config/maps.go` | HasDifferences() method |
| `DBServers/index.jsx` | Table view indicator |
| `DBServerGrid/index.jsx` | Grid view tag |

---

**Quick Links:**
- [Full Documentation](README.md)
- [Run Tests](test_config_diff.sh)
- GitHub Issues: Tag with `config-diff`
