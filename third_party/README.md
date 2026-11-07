# Local driver safety patches

These are minimal source-visible replacements of established upstream modules. The original module cache is unchanged. Runtime source, upstream README, license and module metadata are copied; upstream test suites requiring servers or other fixtures are omitted. Our small regression tests use only in-memory packets and synthetic SCRAM messages.

The root `go.mod` selects these directories with `replace`. Root `go test ./...` does not traverse nested modules; CI must also run the two explicit commands below, serially:

```sh
(cd third_party/mysql && go test ./...)
(cd third_party/scram && go test ./...)
```

## github.com/go-sql-driver/mysql v1.10.1

Upstream: https://github.com/go-sql-driver/mysql/tree/v1.10.1

License: MPL-2.0; the upstream `LICENSE` and source notices are retained unchanged.

Go module checksum: `github.com/go-sql-driver/mysql v1.10.1 h1:arlSnNLq6a5yxGxV7qg9lF4j0C+KwD6NbQyKr9QL6ME=`

Only modified upstream runtime file: `mysql/packets.go`. All exact changed lines are provided in [`mysql/LOCAL_CHANGES.patch`](mysql/LOCAL_CHANGES.patch). Source-file modification notices identify this local change.

Original `packets.go` SHA-256: `996d361199426ff8efa4f090f77be36ac2108e70da28b781eba7f3ea274c7d90`.

Reject server result column counts above 256 before uint64-to-int conversion; validate truncated length encodings and metadata flags; guard readColumns before allocation; reject prepare column/parameter counts above 256 before assigning/converting. New regression test: `metadata_limit_test.go`. This rejects wider queries intentionally.

## github.com/xdg-go/scram v1.2.0

Upstream: https://github.com/xdg-go/scram/tree/v1.2.0

License: Apache-2.0; the upstream `LICENSE` and source notices are retained unchanged.

Go module checksum: `github.com/xdg-go/scram v1.2.0 h1:bYKF2AEwG5rqd1BumT4gAnvwU/M9nBp2pTSxeZw7Wvs=`

Only modified upstream runtime file: `scram/client_conv.go`. All exact changed lines are provided in [`scram/LOCAL_CHANGES.patch`](scram/LOCAL_CHANGES.patch). Source-file modification notices identify this local change.

Original `client_conv.go` SHA-256: `b88fb34e013aaea993b598d2768cf75599b191541e9966eb88d763e5970e50e8`.

Reject server iteration counts above 100000 before getDerivedKeys/PBKDF2. Existing minimum-iteration checks remain. New regression test: `iteration_limit_test.go` checks rejection without starting a hash and supports the exact boundary using a synthetic cached key. Authentication configured above this work factor now fails closed; there is no automatic fallback.

## Redistribution

The modified MySQL runtime file and local MySQL regression test are MPL-2.0 source. This repository includes the modified covered source and patch; preserve its license/notices and the MPL source-availability requirements when distributing binaries. The SCRAM replacement and its local regression test retain Apache-2.0 licensing. These module licenses do not change the application or other dependencies' licenses.
