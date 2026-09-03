# Plugin framework acceptance matrix

This file lists executable evidence rather than treating source presence as a
passing acceptance test.

| Requirement | Automated evidence | Current state |
| --- | --- | --- |
| External directory discovery | `TestStandalonePythonTemplateIsSelfContained` checks a copied manifest; `TestExternalDirectoryProcessPluginCompletesIncrementalRoundTrip` discovers a temporary external directory, starts its executable, performs Health/identity checks, and runs full plus incremental fetch over real TCP gRPC | passing on Windows |
| Standalone repository build | `plugin/templates/datasource-python/Dockerfile` uses only files in that directory; `plugin.yml` builds with that directory as the complete Docker context | automated in Linux Docker CI |
| Complete datasource sync | local-directory gRPC round trip performs full/incremental fetch; `TestPluginIncrementalSyncOnlyReprocessesChangedFile` runs a real gRPC adapter through two host `ProcessSync` passes and the production SQLite knowledge repository | passed on Windows; live parser/index worker E2E remains pending |
| No outbound networking | OCI HostConfig unit test requires `network=none` and AppArmor | passing |
| Blocked attempt is recorded | `plugin/security-probe/verify-linux.sh` performs a real connect and checks kernel audit; manual `apparmor-audit` workflow installs the profile and runs it | executable Linux acceptance job added; an AppArmor-enabled run is still required |
| One changed file only | `TestIncrementalFetchEmitsOnlyChangedFile`, gRPC round trip, and host `TestPluginIncrementalSyncOnlyReprocessesChangedFile` | passed on Windows: two persisted rows remain, only the changed row gets a new ID/hash, and file-save/parser-task counts rise by one |
| Independent implementation from docs | self-contained Python template and README | template present; third-party reproduction pending |
| Runtime lifecycle | SystemAdmin list/enable/disable API, periodic Health, and Manager load/disable/enable tests including datasource and retrieval | implemented for all five external extension types and in-process built-ins |
| Built-in/external lifecycle parity | `BuiltinRegistration` publishes and withdraws datasource, web search, document parser, model provider, and retrieval engine implementations through the same Manager state machine and admin API | implemented; tenant-config-dependent built-ins have no context-free periodic probe and report configuration health on use |
| Web search without core factory edits | typed protocol, external registry metadata, Manager lifecycle, and `mock-web-search` example | implemented; OCI E2E requires Docker CI |
| Document parser without core registry edits | streamed protocol, shared engine registry, Manager lifecycle, and `plain-text-parser` example | implemented; OCI E2E requires Docker CI |
| Model provider without core registry edits | typed metadata/config validation, shared registry, OpenAI-compatible fallback, and `mock-model-provider` example | implemented for OpenAI-compatible transports |
| Retrieval engine without core factory edits | full CRUD/search protocol, public SDK, shared registry and `memory-retrieval` example | implemented; persistent-backend OCI E2E requires Docker CI |
| All requested extension types | datasource, web search, document parser, compatible model providers, and retrieval engines use the unified Manager | implemented; proprietary model transports remain an explicit v1 limitation |
