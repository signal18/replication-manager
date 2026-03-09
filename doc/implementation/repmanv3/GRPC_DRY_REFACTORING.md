# gRPC v3 DRY Refactoring - Complete

## Overview

Successfully refactored the gRPC v3 API to use DRY (Don't Repeat Yourself) principles with generic container types. All methods now use `google.protobuf.Struct` for data responses and `google.protobuf.Empty` for action responses, eliminating code duplication and direct dependencies on internal Go struct types.

## Changes Summary

### 1. Proto File Changes

**File:** `signal18/replication-manager/v3/cluster.proto`

#### Added Import
```protobuf
import "google/protobuf/empty.proto";
```

#### Updated Method Return Types

| Method | Old Return Type | New Return Type |
|--------|----------------|-----------------|
| `MasterPhysicalBackup` | `google.protobuf.Struct` | `google.protobuf.Empty` |
| `SetActionForClusterSettings` | `google.protobuf.Struct` | `google.protobuf.Empty` |
| `PerformClusterAction` | `google.protobuf.Struct` | `google.protobuf.Empty` |

All other methods already used `google.protobuf.Struct` or `stream google.protobuf.Struct`.

---

### 2. DRY Helper Functions

**File:** `server/repmanv3.go`

#### Created/Updated 3 Helper Functions:

##### a) `marshalToStruct()` - Universal Data Converter
```go
// marshalToStruct converts any Go value directly to google.protobuf.Struct
// This is used by all methods that return structured data
// DRY principle: single source of truth for data → Struct conversion
func marshalToStruct(v interface{}) (*structpb.Struct, error)
```

**Used by:**
- GetCluster()
- GetSettingsForCluster()
- ClusterStatus()
- GetClientCertificates()

**Benefit:** Eliminates 8-11 lines of duplicated marshal/unmarshal code per method.

##### b) `marshalAndSend()` - Universal Streaming Converter
```go
// marshalAndSend converts any value to protobuf Struct and sends it via stream
// Handles special cases for strings and string slices automatically
// DRY principle: single source of truth for streaming data conversion
func marshalAndSend(v interface{}, send func(*structpb.Struct) error) error
```

**Used by:**
- GetBackups()
- GetTags()
- GetQueryRules()
- GetSchema()
- RetrieveFromTopology()

**Benefit:** Handles type conversion consistently across all streaming methods.

##### c) `emptyResponse()` - Standard Empty Response
```go
// emptyResponse returns a standard empty protobuf response
// Used by action methods that complete successfully but return no data
// DRY principle: consistent empty response across all action endpoints
func emptyResponse() (*emptypb.Empty, error)
```

**Used by:**
- MasterPhysicalBackup()
- SetActionForClusterSettings()
- PerformClusterAction() (3 return locations)

**Benefit:** Consistent, readable empty responses for all action methods.

---

### 3. Method Refactoring

#### GetCluster() - Simplified

**Before (12 lines):**
```go
b, err := json.Marshal(mycluster)
if err != nil {
    return nil, status.Error(codes.Internal, "could not marshal cluster")
}

out := &structpb.Struct{}
err = protojson.Unmarshal(b, out)
if err != nil {
    return nil, status.Error(codes.Internal, "could not unmarshal json config to struct")
}

return out, nil
```

**After (1 line):**
```go
return marshalToStruct(mycluster)
```

**Reduction:** 11 lines eliminated

---

#### GetSettingsForCluster() - Simplified

**Before (12 lines):**
```go
b, err := json.Marshal(*mycluster.Conf)
if err != nil {
    return nil, status.Error(codes.Internal, "could not marshal config")
}

out := &structpb.Struct{}
err = protojson.Unmarshal(b, out)
if err != nil {
    return nil, status.Error(codes.Internal, "could not unmarshal json config to struct")
}

return out, nil
```

**After (1 line):**
```go
return marshalToStruct(*mycluster.Conf)
```

**Reduction:** 11 lines eliminated

---

#### MasterPhysicalBackup() - Simplified & Changed to Empty

**Before (14 lines, returned Struct with wrapper):**
```go
err = m.JobBackupPhysical()

data := map[string]interface{}{
    "success": err == nil,
    "action":  "master-physical-backup",
}
if err != nil {
    data["error"] = err.Error()
}

return marshalToStruct(data)
```

**After (5 lines, returns Empty):**
```go
err = m.JobBackupPhysical()
if err != nil {
    return nil, err
}

return emptyResponse()
```

**Benefits:**
- 9 lines eliminated
- Semantically correct (action with no data → Empty)
- Uses gRPC status for error handling (standard practice)

---

#### SetActionForClusterSettings() - Changed to Empty

**Before:**
```go
return successResponse("setting-action", in.Cluster.Name, in.Action.String())
```

**After:**
```go
return emptyResponse()
```

**Benefits:**
- Removed unnecessary custom wrapper function
- Consistent with other action methods

---

#### PerformClusterAction() - Fixed 3 Returns

**Changes:**
1. Signature: `*structpb.Struct` → `*emptypb.Empty`
2. Fixed variable declaration (added `err :=` on line 530)
3. Updated 3 return statements:

**Location 1 (ADD action, line 534):**
```go
// Before: return
// After:  return emptyResponse()
```

**Location 2 (SWITCHOVER action, line 617):**
```go
// Before: return
// After:  return emptyResponse()
```

**Location 3 (End of function, line 634):**
```go
// Before: return
// After:  return emptyResponse()
```

---

### 4. Deleted Functions

**Removed wrapper functions that added unnecessary complexity:**
- `successResponse(action, cluster, detail string)` - Created wrapper structure
- `errorResponse(action, cluster, errMsg string)` - Unused (we use gRPC status codes)

---

## Response Structure

### Data Methods → `google.protobuf.Struct`

Methods that return data use `Struct` and directly marshal the data:

**Example Response (GetCluster):**
```json
{
  "Name": "cluster1",
  "Conf": {...},
  "servers": [...],
  "proxies": [...]
}
```

**Example Response (ClusterStatus):**
```json
{
  "alive": "RUNNING",
  "name": "cluster1"
}
```

**Example Response (GetClientCertificates):**
```json
{
  "clientCertificate": "-----BEGIN CERTIFICATE-----\n...",
  "clientKey": "-----BEGIN PRIVATE KEY-----\n...",
  "authority": "-----BEGIN CERTIFICATE-----\n..."
}
```

---

### Streaming Methods → `stream google.protobuf.Struct`

Each streamed item is marshaled directly:

**Example Response (GetBackups):**
```json
{"id": "a1b2c3", "time": "2024-03-09T10:00:00Z", "hostname": "db1"}
{"id": "d4e5f6", "time": "2024-03-08T10:00:00Z", "hostname": "db1"}
{"id": "g7h8i9", "time": "2024-03-07T10:00:00Z", "hostname": "db1"}
```

---

### Action Methods → `google.protobuf.Empty`

Methods that perform actions but return no data use `Empty`:

**Methods:**
- MasterPhysicalBackup
- SetActionForClusterSettings
- PerformClusterAction

**Response:** Empty message (no JSON body)

**Success:** HTTP 200, gRPC status OK  
**Failure:** gRPC status code (e.g., `NOT_FOUND`, `INVALID_ARGUMENT`) with error message

---

## DRY Benefits Achieved

### Code Reduction
| Metric | Count |
|--------|-------|
| Lines eliminated from GetCluster | 11 |
| Lines eliminated from GetSettingsForCluster | 11 |
| Lines eliminated from MasterPhysicalBackup | 9 |
| Wrapper functions deleted | 2 |
| **Total lines reduced** | **~31 lines** |
| Helper functions added | 3 (~40 lines) |
| **Net change** | Slightly more code, but MUCH more maintainable |

### Qualitative Benefits

1. **Single Source of Truth**
   - Marshaling logic in one place (`marshalToStruct`)
   - Streaming logic in one place (`marshalAndSend`)
   - Empty responses in one place (`emptyResponse`)

2. **Consistency**
   - All data methods use same pattern
   - All action methods use same pattern
   - All error handling uses gRPC status codes

3. **Maintainability**
   - Change marshaling behavior in ONE function
   - Add logging/metrics in ONE place
   - Easier to understand method logic (less boilerplate)

4. **Type Safety**
   - Helpers ensure correct conversion every time
   - Compiler catches interface mismatches

5. **Testability**
   - Can unit test helpers independently
   - Easier to mock/stub for testing

---

## Proto Toolchain Setup

### Installed Components

1. **protoc** - Protocol buffer compiler (already installed v3.21.12)
2. **protoc-gen-go** - Go code generator
3. **protoc-gen-go-grpc** - Go gRPC code generator
4. **protoc-gen-grpc-gateway** - gRPC-Gateway generator
5. **protoc-gen-openapiv2** - OpenAPI/Swagger generator

### Installation Commands

```bash
# Install Go plugins
go install google.golang.org/protobuf/cmd/protoc-gen-go@latest
go install google.golang.org/grpc/cmd/protoc-gen-go-grpc@latest
go install github.com/grpc-ecosystem/grpc-gateway/v2/protoc-gen-grpc-gateway@latest
go install github.com/grpc-ecosystem/grpc-gateway/v2/protoc-gen-openapiv2@latest

# Clone googleapis (required for google/api/annotations.proto)
git clone --depth 1 https://github.com/googleapis/googleapis.git
```

### Regenerate Proto Files

```bash
PROTO_DIR=signal18/replication-manager/v3

protoc \
  -I ${PROTO_DIR} \
  -I googleapis/ \
  --go_opt=paths=source_relative \
  --go_out=repmanv3 \
  --go-grpc_opt=paths=source_relative \
  --go-grpc_out=repmanv3 \
  --grpc-gateway_opt logtostderr=true \
  --grpc-gateway_opt paths=source_relative \
  --grpc-gateway_out repmanv3 \
  --openapiv2_out repmanv3 \
  --openapiv2_opt logtostderr=true \
  --openapiv2_opt allow_merge=true \
  --openapiv2_opt merge_file_name=repmanv3 \
  -orepmanv3/service.desc \
  ${PROTO_DIR}/cluster.proto ${PROTO_DIR}/messages.proto
```

---

## Generated Files Updated

- `repmanv3/cluster.pb.go` - Message definitions
- `repmanv3/cluster_grpc.pb.go` - Service interfaces and client/server code
- `repmanv3/cluster.pb.gw.go` - gRPC-Gateway HTTP bindings
- `repmanv3/messages.pb.go` - Message definitions from messages.proto
- `repmanv3/repmanv3.swagger.json` - OpenAPI/Swagger spec
- `repmanv3/service.desc` - Proto descriptor

---

## Client Impact

### Breaking Changes

Clients using the following methods will see interface changes:

1. **MasterPhysicalBackup**
   - Before: Returns JSON `{"success": true/false, "action": "...", "error": "..."}`
   - After: Returns empty message on success, gRPC error on failure

2. **SetActionForClusterSettings**
   - Before: Returns JSON `{"success": true, "action": "...", "cluster": "...", "detail": "..."}`
   - After: Returns empty message on success, gRPC error on failure

3. **PerformClusterAction**
   - Before: Returns JSON `{"success": true, ...}` or empty return
   - After: Returns empty message on success, gRPC error on failure

### Non-Breaking

These methods had NO client-visible changes:
- GetCluster
- GetSettingsForCluster
- ClusterStatus
- GetClientCertificates
- GetBackups
- GetTags
- GetQueryRules
- GetSchema
- RetrieveFromTopology

---

## Testing

### Manual Verification

```bash
# Test data method
grpcurl -plaintext -d '{"name":"cluster1"}' \
  localhost:10005 signal18.replication_manager.v3.ClusterService/GetCluster

# Test action method
grpcurl -plaintext -d '{"name":"cluster1"}' \
  localhost:10005 signal18.replication_manager.v3.ClusterPublicService/MasterPhysicalBackup

# Test streaming method
grpcurl -plaintext -d '{"name":"cluster1"}' \
  localhost:10005 signal18.replication_manager.v3.ClusterService/GetBackups
```

### Expected Results

- **Data methods:** JSON object with data fields
- **Action methods:** Empty response (success) or gRPC error (failure)
- **Streaming methods:** Multiple JSON objects, one per line

---

## Future Improvements

### Potential Further DRY Opportunities

1. **ACL Checking Pattern**
   Many methods repeat:
   ```go
   user, mycluster, err := s.getClusterAndUser(ctx, in)
   if err != nil {
       return nil, err
   }
   if err = user.Granted(config.GrantXYZ); err != nil {
       return nil, err
   }
   ```

   Could create:
   ```go
   func (s *ReplicationManager) getClusterAndCheckGrant(
       ctx context.Context,
       in v3.ContainsClusterMessage,
       grant string,
   ) (cluster.APIUser, *cluster.Cluster, error)
   ```

2. **Password Scrubbing**
   Line 367 has TODO about password scrubbing in GetCluster.
   Could centralize in `marshalToStruct` or create separate helper.

3. **Logging/Metrics**
   Could add structured logging to helpers:
   - Log all marshal operations
   - Track conversion timing
   - Monitor error rates

---

## Conclusion

✅ **Successfully refactored gRPC v3 API using DRY principles**

### Key Achievements:
- Eliminated ~31 lines of duplicated code
- Created 3 reusable helper functions
- Standardized on `google.protobuf.Struct` for data
- Standardized on `google.protobuf.Empty` for actions
- Improved code maintainability and readability
- Followed gRPC best practices

### Architecture:
- **Loose coupling:** Proto doesn't depend on internal Go types
- **Standard patterns:** Uses protobuf standard types
- **Error handling:** Uses gRPC status codes (not in-band errors)
- **Forward compatible:** Can change internal types without breaking API

### Status:
- ✅ Proto files updated
- ✅ Proto code regenerated
- ✅ All methods refactored
- ✅ Helper functions created
- ✅ Documentation complete
- ⏳ Build verification (blocked by pre-existing BackupStat issue)

---

**Date:** 2026-03-09  
**Author:** AI Assistant (Claude)  
**Version:** v3 gRPC API Final
