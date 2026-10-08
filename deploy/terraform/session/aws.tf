# AWS VMs, one module per region: Mumbai holds Act 1; Hyderabad only the etcd witness (Act 2).

module "mumbai" {
  source     = "./modules/aws_site"
  vms        = { for n, v in local.vms : n => v if v.cloud == "aws" && v.region == "ap-south-1" }
  cidr       = "10.10.0.0/16"
  wg_sources = local.wg_sources
  user_data  = local.cloud_init
  ssh_key    = var.admin_ssh_public_key
}

module "hyderabad" {
  source     = "./modules/aws_site"
  providers  = { aws = aws.hyd }
  vms        = { for n, v in local.vms : n => v if v.cloud == "aws" && v.region == "ap-south-2" }
  cidr       = "10.11.0.0/16"
  wg_sources = local.wg_sources
  user_data  = local.cloud_init
  ssh_key    = var.admin_ssh_public_key
}

# The last guardrail: at 100% of the monthly budget (credits excluded), stop every instance.
resource "aws_budgets_budget_action" "stop" {
  for_each           = { for region, ids in { "ap-south-1" = module.mumbai.ids, "ap-south-2" = module.hyderabad.ids } : region => values(ids) if length(ids) > 0 }
  budget_name        = local.p.budget_name
  action_type        = "RUN_SSM_DOCUMENTS"
  approval_model     = "AUTOMATIC"
  notification_type  = "ACTUAL"
  execution_role_arn = local.p.budget_action_role_arn
  action_threshold {
    action_threshold_type  = "PERCENTAGE"
    action_threshold_value = 100
  }
  definition {
    ssm_action_definition {
      action_sub_type = "STOP_EC2_INSTANCES"
      instance_ids    = each.value
      region          = each.key
    }
  }
  subscriber {
    address           = local.p.alert_email
    subscription_type = "EMAIL"
  }
}
