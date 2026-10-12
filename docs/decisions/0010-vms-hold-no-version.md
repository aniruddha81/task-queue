# 0010. VMs hold no version; they converge to the current release at boot

**Status:** accepted · **Decided:** week 10

## Context

If a VM's boot script contained the release, every release would replace every VM, and a rebooted or replaced VM could come back on an old release. Deploying over SSH would need a public SSH port and keys in CI.

## Decision

- cloud-init only bootstraps the VM and contains no version.
- At every boot, `tq-converge.service` reads the VM's own secrets using the VM's own identity (SSM on AWS, Key Vault on Azure), brings up the WireGuard mesh, and runs `deploy.sh` for the release recorded as `current` (kept in both SSM and Key Vault).
- Releases are deployed in place through each cloud's Run Command, never over SSH. There is no public SSH, and admin access goes over the mesh.
- CI signs in to both clouds with GitHub OIDC and holds no stored secrets. Infrastructure changes run from the admin machine (`make infra`), so the release role can't create IAM roles or role assignments.

## Consequences

- **Good:** a new, replaced or rebooted VM comes up on the right release without anyone touching it. Rebuilding a whole session needs no deploy.
- **Good:** no SSH keys or cloud secrets in CI, and nothing public except port 443 and WireGuard.
- **Bad:** Run Command is slow and awkward. Azure's doesn't return a script's exit status, so `vmrun.sh` adds an in-band marker, and AWS's fails if the VM's agent hasn't registered yet. Both caused bugs ([bugs.md](../bugs.md)).

## Evidence

- The session was destroyed and rebuilt twice, and served again within 3–4 minutes each time with no deploy. A deliberately broken release rolled back by itself in 3 m 40 s ([cloud-bringup.md](../results/cloud-bringup.md)).
- `make infra REPLACE=pg-a` replaced the primary with 0 of 24,385 probe requests failed ([act2-database.md](../results/act2-database.md)).
