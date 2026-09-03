# Minimal Python datasource plugin

This directory is self-contained: it does not import WeKnora source and its
Docker build context is this directory alone. Copy it to a new repository,
then change `metadata.id`, `extensionPoints[0].id`, and the matching constants
in `server.py`.

Build it from the copied repository:

```bash
docker build -t example/weknora-local-directory:0.1.0 .
```

Place the repository directory below one of the paths in
`WEKNORA_PLUGIN_DIRS`. WeKnora discovers its `plugin.yaml` on startup. Create a
data source with type `example_local_directory` and `settings.root=/data`.

The example implements a full initial fetch, SHA-256 incremental cursor,
deletion tombstones, configuration validation, resource listing, identity,
and standard gRPC health. Replace `snapshot()` and `Fetch()` to implement a
different source while preserving the RPC contract.

For an OCI plugin declaring `network.outbound: false`, the Linux host must
load WeKnora's `weknora-plugin-no-network` AppArmor profile before startup.
