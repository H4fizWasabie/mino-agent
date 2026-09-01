# Release note: issue #233 cross-platform audit follow-up

One-PR hardening follow-up for native host operations. It fixes service identity comparisons,
fail-closed macOS targeting, Windows rollback safety, package-name compatibility, browser-port
validation, and bounded health probes. It also preserves launchd environment variables and makes
unsupported Windows service fields explicit errors.

Verification passed with the full test suite, race detector, vet, and Linux/macOS/Windows amd64
cross-builds. No production deployment or release tag was performed.
