# MIG execution and recovery

This plugin retains Volcano's existing geometry and slot allocation. The actual
encoding emitted by Volcano `fde2e484326769365f96acf890a176e9bcf869bf` is
`GPU-UUID[group-position]`; the comma example in its comment is not the wire format.
The plugin validates that group and slot against the configured model geometry.
It never selects a substitute slot or changes scheduler resource accounting.

MIG startup and each allocation export the current hardware layout. Export,
parsing, NVML lookup, configuration writes and apply failures return errors
instead of terminating the process. A private configuration file contains only
the empty GPUs that require the assigned layout, so other GPUs are not included
in an apply. Existing nonempty layouts must exactly match. Conflicting layouts,
active GPU processes, or newly observed MIG instances prevent an apply.
Automatic destruction/replacement of existing instances is intentionally excluded;
operators must coordinate such layout changes separately.

After an apply failure the next request re-exports hardware. It never assumes
that a failed command made no changes, and it never rolls back hardware. On
restart an already matching layout is reused without an apply.

With CDI enabled, the existing configured CDI handler publishes specs and the
runtime cache must resolve each exact allocated MIG UUID with a capability device.
This validation runs even without a layout change. Discovery/publication failures
retry five times and then return an error, before consuming the allocation
annotation. UUID device naming is required for this geometry path. Index naming
cannot resolve newly created instances from the startup physical-GPU map.

Local tests cover geometry/slot preservation, conflicting layouts, partial apply,
CDI publication retries, exact CDI membership, configuration write failure and
restart reuse using mocked hardware. They do not establish CUDA execution,
real MIG device order, process isolation or the absence of hardware/driver-specific
reset behavior. Those require an explicitly authorized, idle MIG GPU and an
unmodified Volcano deployment. The plugin must be the sole layout manager during
such tests; external administrators can race hardware changes outside its lock.
