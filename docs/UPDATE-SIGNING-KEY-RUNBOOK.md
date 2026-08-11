# Update signing key rotation and revocation

ScriptBoard release manifests use Ed25519 detached signatures. Release private keys must be generated and retained outside the repository, preferably in an offline or hardware-backed signing environment. Never place a private key in source, build artifacts, logs, caches, or a State Root backup.

## Release environment

The protected `release` environment provides the current `SCRIPTBOARD_UPDATE_KEY_ID`, `SCRIPTBOARD_UPDATE_PUBLIC_KEY`, and `SCRIPTBOARD_UPDATE_SIGNING_KEY`. A planned rotation additionally provides `SCRIPTBOARD_UPDATE_NEXT_KEY_ID` and `SCRIPTBOARD_UPDATE_NEXT_PUBLIC_KEY`; providing `SCRIPTBOARD_UPDATE_NEXT_SIGNING_KEY` makes the manifest carry both current and next signatures. `SCRIPTBOARD_UPDATE_REVOKED_KEY_IDS` is a unique comma-separated list embedded in every release binary. A key cannot be both trusted and revoked in the same build.

## Planned rotation

1. Generate the next Ed25519 key outside GitHub and keep its private material offline.
2. Publish at least one release that embeds the next public key while the current key remains trusted. Sign with the current key; when policy permits the next private key in the protected release environment, provide it to generate a dual-signed manifest.
3. Verify the release manifest with both public keys and run `scriptboard update verify-package` for every platform archive.
4. Promote the next key to current in a later release, remove the old public key, and add the old key ID to `SCRIPTBOARD_UPDATE_REVOKED_KEY_IDS`. Retain the revocation entry in supported releases.
5. Remove the old private key from all signing systems and record its destruction or archival custody in the release evidence.

## Suspected compromise

Pause publishing and preserve workflow, environment, and audit evidence. Remove the exposed private key from the release environment, rotate repository/environment access, generate a new offline key, and publish a clean-checkout release signed only by uncompromised material. Embed the exposed key ID in `SCRIPTBOARD_UPDATE_REVOKED_KEY_IDS`, verify all four platform archives offline, and publish a security advisory with manual verification instructions.

The revocation list is embedded in binaries; it protects clients after they install a release containing that list. A client that only trusts a compromised key cannot safely learn revocation from a manifest signed by that same key. Such clients require an independently authenticated manual upgrade path and out-of-band checksum/key communication.

## Release evidence

Retain the clean commit and tag, manifest and detached signature, signer key IDs, SHA-256 list, SBOM, provenance attestation, offline package-verification output, and the approver identity. Do not retain private key bytes.
