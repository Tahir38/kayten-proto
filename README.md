# kayten-proto

Shared Protobuf definition tree for the Kayten system. Consumed as a git
submodule (`api/proto`) by `kayten-app`, `kayten-app-v2`, and `kayten-server`,
and read directly by `uHSM-Host` / `uHSM-HSM` C code for symbol reference.

This repository is the single source of truth for:

- gRPC service + message definitions (`proto/kayten/v1/`)
- Firmware <-> mobile SPI constants (`proto/kayten/hsm_keys/v1/`)
- Canonical wire<->enum translator reference implementations
  (`references/translators/`)

## Layout

```
proto/
  kayten/
    v1/                          # gRPC services + transport-layer messages
      attestation.proto          # device attestation service
      auth.proto                 # auth / refresh token service
      calling.proto              # call signaling messages
      common.proto               # shared primitives (Timestamp, etc.)
      contacts.proto             # contact discovery service
      conversation.proto         # conversation lifecycle service
      device.proto               # device identity + revocation service
      messaging.proto            # envelope / chat messages
      user.proto                 # user profile + privacy settings
    hsm_keys/
      v1/
        hsm_keys.proto           # SPI constants (CryptoKeyId,
                                 # MobileCommandId, ProvisioningState,
                                 # WipeReason, CryptoKeyIdBandSize)
references/
  translators/                   # canonical wire<->enum translators
    translator.dart              # kayten-app + kayten-app-v2 Dart
    translator.kt                # Android HsmPlugin
    translator.go                # kayten-server
    translator.rs                # kayten-app-v2 rust-core
    translator.swift             # kayten-app-v2 iOS bridge
buf.yaml                         # lint + breaking config
buf.gen.yaml                     # generator config (Go + Dart, for dev only)
docs/superpowers/plans/          # design specs (authoritative)
gen/                             # gitignored; local-only `buf generate` output
```

`gen/` is intentionally gitignored — consumers regenerate into their own
trees (see "Regenerating bindings" below).

## Consuming as a submodule

All three consumer repos pin this repository at `api/proto` and track the
`develop` branch:

```
[submodule "api/proto"]
    path = api/proto
    url = gitea@192.168.2.86:Tahir/kayten-proto.git
    branch = develop
```

### Bumping the submodule

```bash
# In the consumer repo
git submodule update --remote api/proto
git add api/proto
git commit -m "chore(proto): bump api/proto to <short-sha>"
```

Bump order when a change here needs to ripple to consumers:

1. Merge the proto change on `develop` in this repo.
2. Bump `api/proto` in `kayten-server` on a feature branch, regenerate Go
   stubs, commit, PR.
3. Bump `api/proto` in `kayten-app` and `kayten-app-v2` in parallel on
   their own feature branches, regenerate bindings, commit, PR.

Steps 2 and 3 are independent — they just need to pin the same proto SHA so
client and server agree on the wire surface.

## Regenerating bindings

Each consumer owns its own codegen. This repo's `buf.gen.yaml` is a
convenience / verification target only — its output under `gen/` is not
committed.

| Consumer              | Tool                        | Command                            |
|-----------------------|-----------------------------|------------------------------------|
| `kayten-server`       | `buf generate` -> Go        | `./scripts/gen-proto.sh`           |
| `kayten-app` (v1)     | `protoc-gen-dart`           | via the app's generate step        |
| `kayten-app-v2` Rust  | `prost-build` in `build.rs` | `cargo build` regenerates          |
| `kayten-app-v2` iOS   | swift-protobuf              | via the iOS build script           |
| `uHSM-Host`           | n/a (reads symbols only)    | manual alignment with C headers    |

### Local verification

To sanity-check that this repo is self-consistent before pushing:

```bash
buf lint                                    # must exit 0
buf breaking --against '.git#branch=develop' # must exit 0
PATH="$PATH:$HOME/.pub-cache/bin" buf generate
```

Requirements:

- `buf` 1.66+ (`brew install bufbuild/buf/buf`)
- For the Dart plugin: `dart pub global activate protoc_plugin` and ensure
  `$HOME/.pub-cache/bin` is on `PATH` (the generator binary is called
  `protoc-gen-dart`).

## Conventions

### Package names

- `kayten.v1` — gRPC services, transport messages.
- `kayten.hsm_keys.v1` — firmware / SPI constants, no RPCs.

Add new top-level packages under `kayten.<name>.v1` to keep codegen paths
stable.

### Enum naming

New files (`kayten.hsm_keys.v1.*` and any future package) follow strict
[`buf` STANDARD](https://buf.build/docs/lint/rules) conventions:

- Enum type is PascalCase (`ProvisioningState`).
- Every value is prefixed with the full SCREAMING_SNAKE_CASE enum name
  (`PROVISIONING_STATE_PROVISIONED`).
- First value is `<ENUM_NAME>_UNSPECIFIED = 0`.

Pre-existing enums in `kayten.v1.*` (e.g. `EnvelopeType`) use unprefixed
values for historical reasons — see "Lint exceptions" below.

### Wire-vs-enum translation

proto3 forces `_UNSPECIFIED = 0`, so numeric enum values diverge by +1 from
the wire byte they represent. `references/translators/` holds the canonical
mapping in all five consumer languages. Consumers copy these files into
their own trees rather than importing them — keeps build systems free of
cross-repo language-toolchain dependencies.

Adding a new wire-mapped enum? Extend every translator in
`references/translators/` in the same PR so consumers stay in lockstep.

### RPC request/response naming

New RPCs follow `buf` STANDARD: `FooBarRequest` / `FooBarResponse` or
`ServiceNameFooBarRequest` / `...Response`. Each pair is unique per RPC.

## Lint state

`buf.yaml` uses `lint.use: [STANDARD]`. Several files under
`proto/kayten/v1/` carry pre-existing naming debt that causes `buf lint`
to report violations on `develop`:

- `messaging.proto` — `EnvelopeType` enum values are unprefixed.
- `auth.proto`, `contacts.proto`, `device.proto`, `user.proto` —
  request/response types that predate `RPC_REQUEST_STANDARD_NAME` /
  `RPC_RESPONSE_STANDARD_NAME` / `RPC_REQUEST_RESPONSE_UNIQUE`.

Renaming these is a breaking-codegen-name change across all five
consumers, so the cleanup is intentionally deferred to a coordinated
rename cycle (not in scope for this repo alone).

New files (under `kayten.hsm_keys.v1.*` and any future package) are
held to the full STANDARD ruleset by the same config — an unprefixed
enum value or non-standard RPC request name in a new file will surface
in `buf lint` output alongside the pre-existing debt.

## Wire-format stability

This repo uses `buf breaking --against develop` (profile `FILE`) to
guarantee wire-format compatibility across branches. Every PR must keep the
breaking check green. In practice that means:

- Do not renumber existing fields or enum values.
- Do not rename existing messages, enums, services, or RPCs.
- Do not change the type of an existing field.
- Additive changes (new field numbers, new RPCs, new enum values, new
  messages, new files) are always safe.

If a truly breaking change is required, bump the package to `v2` in a new
directory and migrate consumers off `v1` over a release cycle — do not
edit `v1` in place.

## Design specs

Authoritative design documents live under `docs/superpowers/plans/`.
Major cross-repo workstreams currently in flight:

- `2026-04-17-cross-repo-kmobile-manager-pkcs11-retirement-spec.md` —
  retires the PKCS#11 mobile dispatch in favor of direct SPI commands;
  this repo's `kayten.hsm_keys.v1` enums are the constants layer.
- `2026-04-17-kayten-proto-hsm-keys-constants-spec.md` — per-repo spec for
  the `hsm_keys.v1` package and the `DeviceService` self-revoke capability
  RPCs (`IssueSelfRevokeCapability`, `RevokeDeviceByCapability`).
- `2026-04-16-kayten-proto-opk-nvm-band-and-dh4-spec.md` — OPK band
  boundaries + DH4 protocol additions.
- `2026-04-18-two-phase-provisioning-cross-repo-spec.md` — two-phase HSM
  provisioning protocol.
- `2026-04-20-cross-repo-wire-identity-v1-spec.md` — wire-authoritative
  signer identity. Adds `signer_identity_pub` (32-byte Ed25519) to
  `SendMessage` / `ReceiveMessage` / `EditMessage` / `GroupSenderKey`;
  retires `DeviceService.GetIdentityKey` (silent user-id fallback was
  the root cause of multi-device decrypt failures); replaces it with
  strict `GetDeviceIdentityKey` + per-user `ListUserDevices`.
- `2026-04-20-wire-identity-v1-completion-spec.md` (repo root of the
  workspace at `/Users/tahir/Repos/`) — completion plan that extends
  the baseline with the explicit `SERVER_CAPABILITY_WIRE_IDENTITY_V1`
  advertisement on `AuthTokenResponse`, the kayten-app TOFU identity
  cache + `KEY_CHANGE_ALERT` UI, remaining `getIdentityKey` callsite
  migrations, and the full kayten-app-v2 (Rust + native) port.

Read the relevant spec before editing an affected proto — the specs carry
the rationale and cross-repo coordination requirements that never fit in a
`.proto` comment.

## Quick reference

```bash
# Full verification before pushing
buf lint && \
  buf breaking --against '.git#branch=develop' && \
  echo "proto repo is clean"

# Regenerate locally (optional; output is gitignored)
PATH="$PATH:$HOME/.pub-cache/bin" buf generate
```
