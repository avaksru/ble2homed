# PR: fix BTHome parsing crash

This branch fixes a panic when parsing BTHome (FCD2) service data by using the centralized parseBTHomeData function and adding a recover in the BLE advertisement handler to prevent process crash loops.

Summary of changes:
- internal/parser/parser.go: replaced inline FCD2 parsing with parseBTHomeData(sd.Data, now)
- cmd/ble2homed/main.go: added defer/recover in the advertisement handler and imported runtime/debug for stack traces

Please review and merge.
