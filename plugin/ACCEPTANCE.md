# Plugin framework acceptance matrix

This file lists executable evidence rather than treating source presence as a
passing acceptance test.

2026-09-11 proprietary model inference: `TestNativeModelInferenceFactories`
loads a compiled Windows EXE through Manager/stdio gRPC and reaches a custom
HMAC-authenticated HTTP/NDJSON reference service through all five model factories.
Tests cover tools/reasoning/usage, per-request credentials, disable/re-enable,
stream failure, cancellation propagated to HTTP, deadlines and invalid vectors/ranks.
Legacy metadata-only transport remains supported. See [contract and scope](MODEL-INFERENCE.md).

2026-09-11 Windows denial audit acceptance: elevated native isolation tests
received four real WFP CLASSIFY_DROP events for IPv4/IPv6 TCP attempts from a
restricted process and its descendant. A separate production-runtime integration
test asserted OS access denial plus two `plugin.network_denied` JSON records with
the matching plugin ID, package SID, application, address, port and protocol.
No production enforcement code changed. Ordinary-user WFP access remains a
deployment limitation. See [committed raw evidence](../docs/acceptance/windows-network-audit-2026-09-11.md).

2026-09-11 independent Windows repository acceptance: a sibling repository with
its own Git history and fresh Python environment built a native plugin ZIP using
only its own source. The unchanged running Lite host installed and enabled it
through normal APIs. Initial sync imported two documents; unchanged sync processed
zero; changing Alpha updated one while Beta's ID/hash/timestamp stayed unchanged.
Parsing, summary, hybrid search and vector-only search completed successfully.
Host Git state and executable hashes were identical before and after acceptance.
See [committed evidence and scope](../docs/acceptance/windows-independent-plugin-2026-09-11.md).

2026-09-10 controlled HTTP update: host policy tests cover exact hosts/methods,
IPv4/IPv6 and mixed DNS denial, per-hop rebinding, pinned numeric dialing with
TLS verification, redirects, header/cookie isolation, size limits and cancellation.
Go reverse-channel and Python generic-service SDK tests pass. Approval tests
cover stale digests, restart persistence, policy changes and forged bundle records.
`TestWindowsNativeControlledHTTP` passed with the compiled example in a real
restricted process: administrator approval, unapproved-domain rejection, successful
public Feishu request through the host, health, disable and re-enable. Evidence:
`artifacts/controlled-http-native-acceptance.log`. This is not browser-click or OCI
end-to-end acceptance. Python stdio is now implemented: `TestPythonStdioGRPC`
checks concurrent binary messages, flow control, cancellation and reverse HTTP.
`TestWindowsNativePythonPublic` passed with the frozen 0.2.0 Python EXE in a real
restricted process: approval, Feishu Validate/List/Fetch, unchanged second fetch,
unapproved host rejection and re-enable. Evidence: `artifacts/python-public-native-acceptance.log`. See
[controlled HTTP guide](CONTROLLED-HTTP.md).

2026-09-11 deployed Windows acceptance: the running Lite application installed and
approved Python public-link plugin 0.2.0 using normal administrator APIs. Connection
validation succeeded for the supplied Feishu URL; an unapproved host returned 400.
The no-network comparison plugin returned 400 for the same URL. A dedicated KB
completed parsing, embedding/indexing and summary, returned 3 hybrid-search results,
and imported zero additional documents on its second sync. Evidence:
`artifacts/feishu-public-live-sync-acceptance.json`. Browser automation encountered
CDP timeouts, so this evidence is API-based, not a completed UI-click acceptance.

2026-09-10 native Windows update: `TestWindowsNativeNoNetwork` verified OS
denial for TCP, UDP non-delivery against reachable positive controls, and
inherited restrictions in a child process. `TestNativeDirectoryStdioIncremental`
and `TestWindowsNativeManagerDirectoryRoundTrip` passed real pipe-based health,
configuration, full/incremental/unchanged sync, disable and re-enable without
Docker. Supplementary WFP events are not verified on this account: reading the
audit configuration returns Access Denied. Policy-applied logs explicitly carry
`attempt_audit: false`. See [native Windows instructions](WINDOWS-NATIVE.md).

| Requirement | Automated evidence | Current state |
| --- | --- | --- |
| External directory discovery | `TestStandalonePythonTemplateIsSelfContained` checks a copied manifest; `TestExternalDirectoryProcessPluginCompletesIncrementalRoundTrip` discovers a temporary external directory, starts its executable, performs Health/identity checks, and runs full plus incremental fetch over real TCP gRPC | passing on Windows |
| Standalone repository build | independent `WeKnora-LocalDirectory-Plugin` Git repository, fresh venv, vendored Proto/SDK, native EXE ZIP; template Docker context also self-contained | passed on Windows 2026-09-11; Linux Docker CI path separate |
| Complete datasource sync | independent plugin installed through live Lite APIs; full, unchanged and single-file update sync; completed parsing/summary and content-checked hybrid/vector-only retrieval | passed on Windows 2026-09-11; Linux OCI E2E separate |
| No outbound networking | OCI HostConfig unit test requires `network=none` and AppArmor | passing |
| Blocked attempt is recorded | Windows native WFP events and `TestWindowsNativeRuntimeDenialAudit` production JSON sink assertions; Linux `plugin/security-probe/verify-linux.sh` checks kernel audit | passed on Windows with administrator WFP access 2026-09-11; ordinary-user audit remains unavailable; AppArmor-enabled Linux run still required |
| One changed file only | `TestIncrementalFetchEmitsOnlyChangedFile`, gRPC round trip, and host `TestPluginIncrementalSyncOnlyReprocessesChangedFile` | passed on Windows: two persisted rows remain, only the changed row gets a new ID/hash, and file-save/parser-task counts rise by one |
| Independent implementation from docs | self-contained Python template and README | template present; third-party reproduction pending |
| Feishu Wiki external tutorial | `templates/feishu-wiki-python` contains its own Proto, API client, gRPC service, Dockerfile, Chinese tutorial and read-only live smoke script | 16 tests passed on Windows from a copied directory outside the checkout (2026-09-09); real Feishu credentials, OCI execution and host parser/index E2E remain pending |
| Runtime lifecycle | SystemAdmin list/enable/disable API, periodic Health, and Manager load/disable/enable tests including datasource and retrieval | implemented for all five external extension types and in-process built-ins |
| Built-in/external lifecycle parity | `BuiltinRegistration` publishes and withdraws datasource, web search, document parser, model provider, and retrieval engine implementations through the same Manager state machine and admin API | implemented; tenant-config-dependent built-ins have no context-free periodic probe and report configuration health on use |
| Web search without core factory edits | typed protocol, external registry metadata, Manager lifecycle, and `mock-web-search` example | implemented; OCI E2E requires Docker CI |
| Document parser without core registry edits | streamed protocol, shared engine registry, Manager lifecycle, and `plain-text-parser` example | implemented; OCI E2E requires Docker CI |
| Model provider without core registry edits | metadata/config plus `Infer`/`InferStream`, public SDK, five model factories, proprietary HMAC/NDJSON example and standalone scaffolder | Windows native EXE and regression tests passed; individual commercial vendors need their own adapters/acceptance |
| Retrieval engine without core factory edits | full CRUD/search protocol, public SDK, shared registry and `memory-retrieval` example | implemented; persistent-backend OCI E2E requires Docker CI |
| All requested extension types | datasource, web search, document parser, compatible/proprietary model providers, and retrieval engines use the unified Manager | implemented; model inference transport extension verified on Windows |
