# Persistent stack: applied once (make bootstrap), kept across cloud sessions. It holds
# what must outlive VMs: the public name, the Key Vault (soft-delete reserves its name), the
# release record, CI's OIDC trust, and the budget guardrail. Nothing here costs more than
# about $2 a month. The state bucket itself is made by `make bootstrap` (AWS CLI), so the
# bucket never manages its own state.

terraform {
  required_version = ">= 1.11"
  backend "s3" {
    key          = "persistent.tfstate"
    region       = "ap-south-1"
    encrypt      = true
    use_lockfile = true
    # bucket = tq-tfstate-<account id>, passed by make bootstrap
  }
  required_providers {
    aws     = { source = "hashicorp/aws", version = "~> 6.0" }
    azurerm = { source = "hashicorp/azurerm", version = "~> 4.0" }
    azuread = { source = "hashicorp/azuread", version = "~> 3.0" }
  }
}

variable "github_repo_with_ids" {
  description = "owner@owner_id/repo@repo_id, as GitHub's OIDC subject claim spells it"
  default     = "aniruddha81@53252451/task-queue@1409923352"
}

variable "public_name" {
  description = "Traffic Manager relative name: the service is https://<this>.trafficmanager.net"
  default     = "tq-aniruddha81"
}

variable "key_vault_name" {
  description = "Globally unique, 3-24 characters"
  default     = "tq-kv-aniruddha81"
}

variable "alert_email" {
  default = "ar.roy564@gmail.com"
}

variable "aws_monthly_limit" {
  description = "USD per month, credits excluded. At 100% the budget action stops every session EC2 instance."
  default     = 120
}

provider "aws" {
  region = "ap-south-1"
  default_tags { tags = { project = "tq", stack = "persistent" } }
}

provider "azurerm" {
  features {}
}

data "aws_caller_identity" "me" {}
data "azurerm_client_config" "me" {}
data "azurerm_subscription" "me" {}

locals {
  location = "centralindia"
  # GitHub's OIDC subject for jobs that use the `cloud` environment. GitHub now names the
  # owner and repository with their immutable IDs too (owner@id/repo@id), so a renamed or
  # re-created repository can't inherit this trust. The environment's protection rule
  # (deployment branches: release only) is what limits it to `release`.
  github_subject = "repo:${var.github_repo_with_ids}:environment:cloud"
}

# ---------- Release record: which commit is live. VMs read `current` at every boot. ----------

resource "aws_ssm_parameter" "release" {
  for_each = toset(["current", "previous", "target"])
  name     = "/tq/release/${each.key}"
  type     = "String"
  value    = "none"
  lifecycle { ignore_changes = [value] } # written by rollout.sh
}

# ---------- Azure: resource groups, Key Vault, Traffic Manager ----------

resource "azurerm_resource_group" "persistent" {
  name     = "tq-persistent"
  location = local.location
}

# Empty until a session; it lives here so CI's role can be scoped to it once.
resource "azurerm_resource_group" "session" {
  name     = "tq-session"
  location = local.location
}

resource "azurerm_key_vault" "kv" {
  name                       = var.key_vault_name
  location                   = local.location
  resource_group_name        = azurerm_resource_group.persistent.name
  tenant_id                  = data.azurerm_client_config.me.tenant_id
  sku_name                   = "standard"
  rbac_authorization_enabled = true
  soft_delete_retention_days = 7
  purge_protection_enabled   = false # data is disposable; session secrets are purged on destroy
}

resource "azurerm_role_assignment" "kv_admin_me" {
  scope                = azurerm_key_vault.kv.id
  role_definition_name = "Key Vault Administrator"
  principal_id         = data.azurerm_client_config.me.object_id
}

resource "azurerm_key_vault_secret" "release" {
  for_each     = toset(["current", "previous", "target"])
  name         = "release-${each.key}"
  value        = "none"
  key_vault_id = azurerm_key_vault.kv.id
  lifecycle { ignore_changes = [value] } # written by rollout.sh
  depends_on = [azurerm_role_assignment.kv_admin_me]
}

# MultiValue: DNS answers carry every healthy gateway IP, so a client whose first
# connection fails tries the next at once. Endpoints are added by the session stack.
resource "azurerm_traffic_manager_profile" "tm" {
  name                   = "tq"
  resource_group_name    = azurerm_resource_group.persistent.name
  traffic_routing_method = "MultiValue"
  max_return             = 2
  dns_config {
    relative_name = var.public_name
    ttl           = 10
  }
  monitor_config {
    protocol                     = "HTTPS" # Traffic Manager doesn't validate the certificate
    port                         = 443
    path                         = "/readyz"
    interval_in_seconds          = 10
    timeout_in_seconds           = 5
    tolerated_number_of_failures = 1
  }
}

# ---------- CI: GitHub OIDC, no stored secrets ----------

resource "aws_iam_openid_connect_provider" "github" {
  url            = "https://token.actions.githubusercontent.com"
  client_id_list = ["sts.amazonaws.com"]
}

resource "aws_iam_role" "github_release" {
  name = "tq-github-release"
  assume_role_policy = jsonencode({
    Version = "2012-10-17"
    Statement = [{
      Effect    = "Allow"
      Principal = { Federated = aws_iam_openid_connect_provider.github.arn }
      Action    = "sts:AssumeRoleWithWebIdentity"
      Condition = {
        StringEquals = {
          "token.actions.githubusercontent.com:aud" = "sts.amazonaws.com"
          "token.actions.githubusercontent.com:sub" = local.github_subject
        }
      }
    }]
  })
}

# Deploy only: run commands on session instances, read the topology, write the release record.
resource "aws_iam_role_policy" "github_release" {
  role = aws_iam_role.github_release.id
  policy = jsonencode({
    Version = "2012-10-17"
    Statement = [
      {
        Effect    = "Allow"
        Action    = "ssm:SendCommand"
        Resource  = "arn:aws:ec2:*:${data.aws_caller_identity.me.account_id}:instance/*"
        Condition = { StringEquals = { "ssm:resourceTag/stack" = "session" } }
      },
      {
        Effect   = "Allow"
        Action   = "ssm:SendCommand"
        Resource = "arn:aws:ssm:*::document/AWS-RunShellScript"
      },
      {
        Effect   = "Allow"
        Action   = ["ssm:GetCommandInvocation", "ec2:DescribeInstances"]
        Resource = "*"
      },
      {
        Effect = "Allow"
        Action = ["ssm:GetParameter", "ssm:PutParameter"]
        Resource = [
          "arn:aws:ssm:ap-south-1:${data.aws_caller_identity.me.account_id}:parameter/tq/release/*",
          "arn:aws:ssm:ap-south-1:${data.aws_caller_identity.me.account_id}:parameter/tq/ci/*",
        ]
      },
    ]
  })
}

resource "azuread_application" "github_release" {
  display_name = "tq-github-release"
}

resource "azuread_service_principal" "github_release" {
  client_id = azuread_application.github_release.client_id
}

resource "azuread_application_federated_identity_credential" "github_release" {
  application_id = azuread_application.github_release.id
  display_name   = "github-release"
  audiences      = ["api://AzureADTokenExchange"]
  issuer         = "https://token.actions.githubusercontent.com"
  subject        = local.github_subject
}

# Run Command on session VMs, and the release record in Key Vault.
resource "azurerm_role_assignment" "github_vms" {
  scope                = azurerm_resource_group.session.id
  role_definition_name = "Virtual Machine Contributor"
  principal_id         = azuread_service_principal.github_release.object_id
}

resource "azurerm_role_assignment" "github_release_record" {
  for_each             = azurerm_key_vault_secret.release
  scope                = each.value.resource_versionless_id
  role_definition_name = "Key Vault Secrets Officer"
  principal_id         = azuread_service_principal.github_release.object_id
}

# ---------- Budget guardrail: credits excluded, so it measures what would really be billed ----------

resource "aws_budgets_budget" "tq" {
  name         = "tq"
  budget_type  = "COST"
  limit_amount = var.aws_monthly_limit
  limit_unit   = "USD"
  time_unit    = "MONTHLY"
  cost_types {
    include_credit = false
    include_refund = false
  }
  dynamic "notification" {
    for_each = [25, 50, 80]
    content {
      comparison_operator        = "GREATER_THAN"
      threshold                  = notification.value
      threshold_type             = "PERCENTAGE"
      notification_type          = "ACTUAL"
      subscriber_email_addresses = [var.alert_email]
    }
  }
}

# The session stack attaches the stop-instances action (it knows the instance IDs).
resource "aws_iam_role" "budget_action" {
  name = "tq-budget-action"
  assume_role_policy = jsonencode({
    Version   = "2012-10-17"
    Statement = [{ Effect = "Allow", Principal = { Service = "budgets.amazonaws.com" }, Action = "sts:AssumeRole" }]
  })
}

resource "aws_iam_role_policy_attachment" "budget_action" {
  role       = aws_iam_role.budget_action.name
  policy_arn = "arn:aws:iam::aws:policy/AWSBudgetsActions_RolePolicyForResourceAdministrationWithSSM"
}

# ---------- Outputs: read by the session stack, and set as GitHub variables by make bootstrap ----------

output "aws_role_arn" { value = aws_iam_role.github_release.arn }
output "azure_client_id" { value = azuread_application.github_release.client_id }
output "azure_tenant_id" { value = data.azurerm_client_config.me.tenant_id }
output "azure_subscription_id" { value = data.azurerm_subscription.me.subscription_id }
output "key_vault_id" { value = azurerm_key_vault.kv.id }
output "key_vault_name" { value = azurerm_key_vault.kv.name }
output "release_current_secret_id" { value = azurerm_key_vault_secret.release["current"].resource_versionless_id }
output "traffic_manager_profile_id" { value = azurerm_traffic_manager_profile.tm.id }
output "public_fqdn" { value = azurerm_traffic_manager_profile.tm.fqdn }
output "session_resource_group" { value = azurerm_resource_group.session.name }
output "budget_name" { value = aws_budgets_budget.tq.name }
output "budget_action_role_arn" { value = aws_iam_role.budget_action.arn }
output "alert_email" { value = var.alert_email }
