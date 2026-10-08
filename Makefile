# Cloud lifecycle (Linux, macOS or WSL, signed in to the aws, az and gh CLIs; jq and curl):
#   make bootstrap   once: the state bucket, the persistent stack, CI's GitHub variables
#   make up          start a cloud phase: VMs, mesh and firewalls (the session stack)
#   make down        end it: destroy the session stack, then sweep
#   make stop/start  nightly before go-live: stop AWS instances, deallocate Azure VMs
#   make sweep       fail if anything billable is left outside the persistent stack
# Local:
#   make torture     the chaos test against local Compose (docs/results/)
#
# Optional, in deploy/terraform/session/terraform.tfvars (git-ignored): admin_wg_public_key
# and admin_ssh_public_key, for admin access over the mesh.

SHELL := bash
.SHELLFLAGS := -euo pipefail -c
STATE = tq-tfstate-$(shell aws sts get-caller-identity --query Account --output text)
TF = ARM_SUBSCRIPTION_ID=$$(az account show --query id -o tsv) terraform -chdir=deploy/terraform
P = $(TF)/persistent
S = $(TF)/session

.PHONY: bootstrap up down stop start sweep torture

bootstrap:
	aws s3api head-bucket --bucket $(STATE) 2>/dev/null || \
	  aws s3api create-bucket --bucket $(STATE) --region ap-south-1 --create-bucket-configuration LocationConstraint=ap-south-1
	aws s3api put-bucket-versioning --bucket $(STATE) --versioning-configuration Status=Enabled
	aws s3api put-public-access-block --bucket $(STATE) --public-access-block-configuration \
	  BlockPublicAcls=true,IgnorePublicAcls=true,BlockPublicPolicy=true,RestrictPublicBuckets=true
	$(P) init -input=false -backend-config=bucket=$(STATE)
	$(P) apply
	for v in aws_role_arn azure_client_id azure_tenant_id azure_subscription_id; do \
	  gh variable set "$${v^^}" --body "$$($(P) output -raw $$v)"; done
	@echo "Once, by hand: GitHub > Settings > Environments > cloud > deployment branches: release only;"
	@echo "and after the first release, make each ghcr.io/aniruddha81/task-queue/* package public."

up:
	$(S) init -input=false -backend-config=bucket=$(STATE)
	$(S) apply -var admin_cidr=$$(curl -fsS https://checkip.amazonaws.com)/32

down:
	$(S) init -input=false -backend-config=bucket=$(STATE)
	$(S) destroy -var admin_cidr=$$(curl -fsS https://checkip.amazonaws.com)/32
	bash deploy/terraform/vms.sh sweep

stop start sweep:
	bash deploy/terraform/vms.sh $@

torture:
	go run ./cmd/torture
