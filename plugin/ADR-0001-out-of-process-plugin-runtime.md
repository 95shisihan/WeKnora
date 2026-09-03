# ADR-0001: Out-of-process plugin runtime

- Status: accepted for v1alpha1
- Scope: datasource, web search, document parser, retrieval engine, and
  OpenAI-compatible model provider implemented; proprietary model transports remain

## Decision

WeKnora plugins use a declarative `plugin.yaml` plus versioned gRPC services.
Development may use a host process over TCP. Production plugins run as OCI
containers and communicate over a private Unix domain socket mounted into the
container. Built-in adapters remain in-process, but business registries consume
the same adapter interfaces and metadata shape used by external plugins.

Each extension type gets a typed protobuf service. The framework does not use a
single `Invoke(method, json)` RPC: that would move compatibility errors from
code generation/startup into production requests and make streaming contracts
hard to validate. Connector-specific configuration and cursor payloads remain
JSON inside typed envelopes so they can evolve without a protocol release.

## Alternatives

### Compile-time unified metadata only

This would make built-ins consistent and is useful internally, but an external
implementation would still require linking it into WeKnora and rebuilding the
main image. It does not satisfy independent-repository installation or runtime
permission enforcement.

### Go `plugin` shared objects

Rejected because they require the same Go toolchain, dependency graph, target
OS, architecture, and ABI as the host. They are not suitable for the Python
docreader boundary, do not support other implementation languages, and execute
with the host process's full memory and filesystem authority.

### Generic subprocess over stdin/stdout

Portable, but health checking, cancellation, bidirectional streaming, schema
generation, and compatibility tooling would all need custom framing. gRPC
already provides these primitives and mature generators in multiple languages.

### TCP-only gRPC

Retained only for development. A plugin that declares no outbound networking
cannot use TCP as its control channel while also being placed in a network-less
namespace. OCI plus Unix sockets separates the control plane from internet
access.

### WebAssembly

Promising for deterministic compute extensions, especially parsers, but not a
good v1 baseline for connectors and model providers that need large SDKs,
streaming clients, native libraries, and filesystem access. A future WASI
runtime can implement the same lifecycle contract.

## Security consequences

OCI plugins receive only declared read-only bind mounts. Their root filesystem
is read-only, capabilities are dropped, privilege escalation is disabled, and
CPU/memory/PID limits are mandatory. `network.outbound=false` applies both a
Docker network namespace with no external interface and an AppArmor profile
that audits and denies IPv4/IPv6 socket creation. Missing enforcement is a
startup error rather than a permissive fallback.

TCP process plugins are explicitly not a security boundary. The host rejects a
no-network declaration for that runtime.

## Compatibility consequences

The manifest API and every extension protocol have independent versions.
Plugin identity, version, protocol version, and extension ID are checked over
gRPC after Health succeeds. Breaking protobuf changes require a new protocol
version; additive fields remain backward compatible. The host version range is
evaluated before any plugin code starts.

## Remaining work

`datasource/v1`, `web_search/v1`, `document_parser/v1`, `retrieval_engine/v1`,
and the metadata/config portion of `model_provider/v1` have typed protocols,
host adapters, Manager lifecycle/health integration, administrator enable/disable
wiring, and OCI examples. Model provider v1 intentionally supports only the
host's OpenAI-compatible transport; proprietary model transports still require
a typed invocation/streaming contract.

The five extension types share one Manager state machine. In-process built-ins
use `BuiltinRegistration` callbacks to publish and withdraw their existing
implementations from the business registries; external plugins use the same
state and administrator API while the Manager additionally owns their gRPC/OCI
runtime. Built-ins that need tenant credentials cannot provide a meaningful
process-wide probe, so registration is their context-free health boundary and
tenant-specific connectivity remains validated on use.

Enable/disable overrides remain process-local in v1alpha1. Persisting an
administrator override across restart, and proprietary non-OpenAI model
transport protocols, are follow-up compatibility work rather than lifecycle
registration gaps.
