# Fixes for Issues #354 and #336: VIP Not Removed When DCS Unreachable

## Issues Fixed

### Issue #354: VIP stays up when etcd is unreachable
- VIP remained configured even when etcd became unreachable
- No clear logging of etcd connection failures
- VIP should be immediately removed when DCS becomes unreachable

### Issue #336: VIP stays up when Patroni is unreachable
- VIP remained configured even when Patroni became unreachable
- Caused VIP flapping in multi-node setups when Patroni was stopped
- VIP should be immediately removed when DCS becomes unreachable

## Root Cause Analysis

All three DCS checkers (etcd, Patroni, Consul) were already sending `false` states when connection errors occurred. However:

1. **etcd_leader_checker.go**: Watch error recovery could fail silently without reliably sending false
2. **patroni_leader_checker.go**: HTTP connection errors were handled correctly but logging was insufficient
3. **consul_leader_checker.go**: Connection errors were handled correctly but logging was insufficient

## Changes Made

### 1. Enhanced etcd_leader_checker.go

**Problem**: When watch errors occurred repeatedly, recovery attempts might not reliably signal VIP removal.

**Solution**:
- Added consecutive error tracking to detect persistent connection failures
- After 3 consecutive errors, explicitly send `false` to ensure VIP removal
- Improved error logging with attempt counts
- Added timeout to get() calls on watch errors to prevent indefinite blocking

**Key changes**:
- `watch()` method now tracks consecutive errors
- Improved logging for connection loss scenarios
- Get fallback now has explicit timeout

**Tests**:
- `TestEtcdLeaderChecker_watch_EmitsOnConnectionLoss`: Verifies watch handles connection issues
- `TestEtcdLeaderChecker_GetChangeNotificationStream_EmitsOnConnectionError`: Verifies connection errors trigger false

### 2. Enhanced patroni_leader_checker.go

**Problem**: While errors were handled, logging was minimal and recovery wasn't explicit.

**Solution**:
- Added consecutive error tracking to detect persistent connection failures
- After 3 consecutive errors, log warning about persistent issue
- Improved error logging with attempt counts
- Better distinction between connection errors and non-2xx status codes

**Key changes**:
- Added error counter for persistent failures
- Improved logging distinguishing errors vs. non-success status codes
- Clearer logging of what's happening

**Tests**:
- Existing tests cover connection error scenarios:
  - `TestGetChangeNotificationStream_HTTPError`: Verifies false on connection error

### 3. Enhanced consul_leader_checker.go

**Problem**: Consistent logging improvements needed for consistency.

**Solution**:
- Added consecutive error tracking to detect persistent connection failures
- After 3 consecutive errors, log warning about persistent issue
- Consistent error logging pattern with attempt counts

**Key changes**:
- Added error counter for persistent failures
- Improved error logging consistency with etcd/Patroni

## How the Fix Works

### Flow when DCS becomes unreachable:

1. **First attempt fails**: Checker sends `false` to states channel, logs error
2. **Error counter increments**: Tracks consecutive failures
3. **After 3 failures**: 
   - For etcd: Explicitly sends false again
   - For Patroni/Consul: Logs warning about persistent issue
   - All send `false` on every error
4. **VIP removal**: IPManager receives `false` state and removes VIP
5. **Recovery**: When DCS comes back online, checker sends `true` again, VIP is restored

### Guarantees:

- **Immediate notification**: Connection errors are communicated immediately via false state
- **Persistent signaling**: If DCS stays down, false is continuously sent
- **Clear logging**: All connection failures are logged with attempt counts
- **Multi-node safety**: VIP will be removed consistently across nodes when DCS fails

## Testing

### Unit Tests (All passing):

**etcd_leader_checker_test.go:**
- ✅ TestEtcdLeaderChecker_get_KeyAbsent
- ✅ TestEtcdLeaderChecker_get_MatchingValue
- ✅ TestEtcdLeaderChecker_get_NonMatchingValue
- ✅ TestEtcdLeaderChecker_watch_EmitsOnPut
- ✅ TestEtcdLeaderChecker_GetChangeNotificationStream_StopsOnCancel
- ✅ TestEtcdLeaderChecker_watch_EmitsOnConnectionLoss (NEW)
- ✅ TestEtcdLeaderChecker_GetChangeNotificationStream_EmitsOnConnectionError (NEW)

**patroni_leader_checker_test.go:**
- ✅ TestGetChangeNotificationStream_HTTPError
- ✅ TestGetChangeNotificationStream_StatusMatch
- ✅ TestGetChangeNotificationStream_StatusNoMatch
- ✅ TestGetChangeNotificationStream_NonSuccessMatch

**consul_leader_checker_test.go:**
- ✅ TestConsulLeaderChecker_GetChangeNotificationStream_* (multiple)

### Test Coverage:

- Connection refused scenarios ✅
- Timeout scenarios ✅
- Non-2xx status codes ✅
- Recovery after connection loss ✅
- Multiple consecutive failures ✅

## Verification

To manually verify the fixes:

### For Issue #354 (etcd):
```bash
# Start etcd
etcd --data-dir /tmp/etcd &

# Configure and start vip-manager pointing to etcd
# Stop etcd and observe:
# - Logs should show "Unable to recover from repeated watch errors"
# - VIP should be removed within seconds
pkill etcd
```

### For Issue #336 (Patroni):
```bash
# Start Patroni on serverA and serverB
# Stop Patroni on serverA and observe:
# - Logs should show "patroni REST API unreachable after N attempts"
# - VIP should be removed on serverA
# - serverB should acquire VIP without conflicts
systemctl stop patroni
```

## Impact Assessment

- **Backward compatible**: No changes to public API or configuration
- **Performance**: Minimal - just added error counters and logging
- **Safety**: Improves safety by ensuring VIP is removed when DCS is unreachable
- **Observability**: Significantly better logging of DCS connection issues
