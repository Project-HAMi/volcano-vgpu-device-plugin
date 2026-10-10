# vGPU cache lifecycle

The main GPU device plugin owns `/vgpu/containers` beneath `HOOK_PATH`.
Each cache directory is named `<pod UID>_<container name>`. Allocate must
successfully create or reuse the directory and set its permissions before returning its mount or consuming the allocation annotation.
Repeated allocation preserves the directory and reuses its one existing cache
file, or selects `shared.cache` if none exists yet. Multiple cache files are
rejected as ambiguous. Only garbage collection deletes cache directories.
`HOOK_PATH` must be absolute and `NODE_NAME` must identify the plugin's node.

The plugin checks its node-scoped Pod informer every five seconds. Garbage
collection requires both an informer snapshot and a fresh API-server list to
confirm the Pod is absent. API errors preserve all candidate directories. A
five-minute directory grace period protects recent preparations. Existing Pods,
including terminating and completed Pods not yet deleted, retain their caches.
Delete completed workload Pods to allow reclamation.

Preparation and deletion share a mutex; API confirmation releases that mutex.
Any intervening preparation invalidates the GC batch. GC rechecks directory
identity and modification time before removal, and ignores malformed entries,
symlinks, and files. Failed confirmation rounds back off up to one minute.
An undeletable directory is retried at most five times per identity, then logged
for manual repair or a plugin restart. Restart resumes collection with fresh
Pod state; it does not clear all caches.

The monitor never deletes cache directories. It releases mappings when a Pod
or directory disappears, or the directory is replaced. Metrics collection and
feedback hold the mapping lifetime lock so GC-related updates cannot unmap
memory still in use by those callbacks.

Implementation and safety tests are adapted from HAMi commit
`3307b6cea526e9b63f5db28ff8dfa2f7b02a1ad1`, with monitor lifetime protection and
Volcano plugin startup/Allocate integration. No scheduler or annotation format
changes are needed. Local filesystem and mocked Kubernetes tests do not prove
CUDA or GPU isolation; those require separate hardware validation.
