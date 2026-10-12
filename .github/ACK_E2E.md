# ACK E2E execution controls

Real ACK runtime validation is currently deferred under #5/#23. On 2026-10-12,
the repository registrations for `E2ESoakTrigger` and `E2EScaleTrigger` were
disabled to stop their existing main-branch schedules. This repository setting
does not change main source. Historical failed runs remain as audit evidence.

The dev workflow fix removes both cron schedules and keeps manual entry points.
The shared ACK suite job requires both `workflow_dispatch` and the repository
variable `ACK_E2E_ENABLED` set to the exact string `true`. Missing/false opt-in
skips the cloud suite job before runner/toolchain allocation. This does not
affect ordinary offline CI or KWOK.

After selecting a dedicated ACK environment and planning the cloud run:

1. Use the reviewed workflow/controller/Chart/CRD commit. These changes are on
   dev until the later main integration; merely merging dev does not update
   main's workflows.
2. Configure the required E2E configuration and credentials through repository
   secrets. `ALIBABA_CLOUD_E2E_CONFIG` must contain a valid ACK configuration,
   including `alibaba_cloud.region_id` and a confirmed Kubernetes version >=1.34.
   The shared runner rejects missing or blank config/credential inputs before
   checkout or Go installation, without printing values. Full configuration
   validation still runs before ACK setup; nonempty input is not validation.
3. Set `ACK_E2E_ENABLED=true` and enable only the intended workflow registration.
   This alone does not start a run. Dispatch the selected reviewed ref manually.
   Until the no-cron changes reach main, keep the two periodic registrations
   disabled: enabling their old main definitions would restore their schedules.
4. Preserve diagnostics and cleanup evidence, then clear the opt-in when the
   planned validation ends. Do not enable or rerun cloud jobs just to remove
   red historical entries from the Actions list.

Failure hooks locate the planned manifest on disk. A failure before setup emits
no manifest and skips dump/upload/cleanup. A partial setup that persisted a
manifest still receives failure diagnostics and requested cleanup, even if the
setup step failed. Cleanup is not conditioned on successful setup.

Issue #81 / milestone **ACK E2E workflow controls** tracks this fix. It is an
Actions/documentation change with lightweight shell/script/workflow validation;
its PR does not build/test/scan Go or execute cloud E2E.
