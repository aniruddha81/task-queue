# terraform -chdir=deploy/terraform/session test
# Runs the session stack against mocked providers (no cloud, no credentials) and checks
# how the role map is wired: who calls whom, rollout order, and who receives which secret.

# Mocked values must look real where the providers validate them (IPs, Azure resource IDs).
mock_provider "aws" {
  mock_resource "aws_eip" {
    defaults = { public_ip = "203.0.113.10" }
  }
}
mock_provider "aws" {
  alias = "hyd"
  mock_resource "aws_eip" {
    defaults = { public_ip = "203.0.113.20" }
  }
}
mock_provider "azurerm" {
  mock_resource "azurerm_public_ip" {
    defaults = {
      id         = "/subscriptions/00000000-0000-0000-0000-000000000000/resourceGroups/tq-session/providers/Microsoft.Network/publicIPAddresses/vm"
      ip_address = "198.51.100.10"
    }
  }
  mock_resource "azurerm_network_interface" {
    defaults = {
      id                 = "/subscriptions/00000000-0000-0000-0000-000000000000/resourceGroups/tq-session/providers/Microsoft.Network/networkInterfaces/vm"
      private_ip_address = "10.20.1.4"
    }
  }
  mock_resource "azurerm_network_security_group" {
    defaults = { id = "/subscriptions/00000000-0000-0000-0000-000000000000/resourceGroups/tq-session/providers/Microsoft.Network/networkSecurityGroups/tq-vms" }
  }
  mock_resource "azurerm_linux_virtual_machine" {
    defaults = {
      id       = "/subscriptions/00000000-0000-0000-0000-000000000000/resourceGroups/tq-session/providers/Microsoft.Compute/virtualMachines/vm"
    }
  }
  mock_resource "azurerm_key_vault_secret" {
    defaults = { resource_versionless_id = "/subscriptions/00000000-0000-0000-0000-000000000000/resourceGroups/tq-persistent/providers/Microsoft.KeyVault/vaults/tq-kv-test/secrets/vm" }
  }
  mock_resource "azurerm_subnet" {
    defaults = { id = "/subscriptions/00000000-0000-0000-0000-000000000000/resourceGroups/tq-session/providers/Microsoft.Network/virtualNetworks/tq/subnets/vms" }
  }
}
mock_provider "tls" {
  mock_resource "tls_private_key" {
    defaults = { public_key_openssh = "ssh-rsa AAAAB3NzaC1yc2EAAAADAQABAAABAQDUfbG4woN/g5pR/N+lzr/v+sy+hvgOT+M9VGmA1IgyB6bXSr4NL4PJHaqd/zM5xdo4jqXEO0gZIvLFiX/JG60y77v5OQDwGabFSWpcn18fXNiKhJJW8KgQHC85LuZKhRY0+CypF/gwvASH9GiLUKm34TH5CuaJHqKzdDDnCF1lOWgEUVBZVJzV9Ut6ibJl1Kd725P00Q5a0P0DvKmCdQVVIARHjCzuleAYLHlS60284KDZqW0smPf+EhSUXXs2ZDg75oFeqgA8ABrjFlhkGymH4PZGohLBGkFLa8wus40XxC3TjaqLjrAqZUq1f9GHTRN0U7Z3YPFyiJ47k5VtCESj mock" }
  }
}
mock_provider "random" {}
mock_provider "wireguard" {}

override_data {
  target = data.terraform_remote_state.persistent
  values = {
    outputs = {
      public_fqdn                = "tq-test.trafficmanager.net"
      key_vault_name             = "tq-kv-test"
      key_vault_id               = "/subscriptions/0/resourceGroups/tq-persistent/providers/Microsoft.KeyVault/vaults/tq-kv-test"
      release_current_secret_id  = "/subscriptions/0/resourceGroups/tq-persistent/providers/Microsoft.KeyVault/vaults/tq-kv-test/secrets/release-current"
      traffic_manager_profile_id = "/subscriptions/0/resourceGroups/tq-persistent/providers/Microsoft.Network/trafficManagerProfiles/tq"
      session_resource_group     = "tq-session"
      budget_name                = "tq"
      budget_action_role_arn     = "arn:aws:iam::123456789012:role/tq-budget-action"
      alert_email                = "ops@example.com"
    }
  }
}

run "stateless_twins" { # the default role map: week 12
  command = apply

  assert {
    condition     = [for v in jsondecode(aws_ssm_parameter.topology.value).vms : v.name] == ["pg-a", "ops-a", "svc-a", "svc-z", "work-a", "work-z"]
    error_message = "rollout order: data, ops, the service VMs one at a time, workers"
  }
  assert {
    condition     = [for v in jsondecode(aws_ssm_parameter.topology.value).vms : v.stage] == ["data", "app", "app", "app", "app", "app"]
    error_message = "only database VMs are in the data stage"
  }
  assert {
    condition     = strcontains(aws_ssm_parameter.file["svc-a/tq.env"].value, "AUTH_URL='https://svc-a:8083,https://svc-z:8083'")
    error_message = "a gateway calls its own cloud's auth, with the other cloud's as its twin"
  }
  assert {
    condition     = strcontains(jsondecode(azurerm_key_vault_secret.vm["svc-z"].value)["tq.env"], "JOBS_URL='https://svc-z:8080,https://svc-a:8080'")
    error_message = "the Azure gateway calls jobs in Azure first"
  }
  assert {
    condition     = strcontains(aws_ssm_parameter.file["svc-a/tq.env"].value, "JWKS_URL='https://svc-a:8083/.well-known/jwks.json,https://svc-z:8083/.well-known/jwks.json'")
    error_message = "jobs can fetch the signing keys from either auth"
  }
  assert {
    condition     = jsondecode(aws_ssm_parameter.topology.value).tm != ""
    error_message = "rollout.sh knows the Traffic Manager profile, to drain gateways"
  }
  assert {
    condition     = strcontains(aws_ssm_parameter.file["svc-a/tq.env"].value, "ACME_DOMAIN='tq-test.trafficmanager.net'")
    error_message = "the gateway gets a certificate for the public name"
  }
  assert {
    condition     = strcontains(aws_ssm_parameter.file["work-a/tq.env"].value, "DISPATCHERS='https://svc-a:8081,https://svc-z:8081'")
    error_message = "an AWS worker tries its own cloud's dispatcher first"
  }
  assert {
    condition     = strcontains(jsondecode(azurerm_key_vault_secret.vm["work-z"].value)["tq.env"], "DISPATCHERS='https://svc-z:8081,https://svc-a:8081'")
    error_message = "an Azure worker tries its own cloud's dispatcher first"
  }
  assert {
    condition     = !strcontains(aws_ssm_parameter.file["work-a/tq.env"].value, "DB_URL") && !strcontains(aws_ssm_parameter.file["work-a/tq.env"].value, "PASSWORD")
    error_message = "workers never receive database credentials"
  }
  assert {
    condition     = !strcontains(aws_ssm_parameter.file["ops-a/tq.env"].value, "JWT_PRIVATE_KEY") && !strcontains(aws_ssm_parameter.file["work-a/tq.env"].value, "JWT_PRIVATE_KEY")
    error_message = "only auth's VMs hold the signing key"
  }
  assert {
    condition     = strcontains(aws_ssm_parameter.file["pg-a/tq.env"].value, "ETCD_INITIAL_CLUSTER='pg-a=http://pg-a:2380'")
    error_message = "one database until week 13: a one-member etcd on pg-a"
  }
  assert {
    condition     = contains(keys(aws_ssm_parameter.file), "ops-a/prometheus.yml") && !contains(keys(aws_ssm_parameter.file), "svc-a/prometheus.yml")
    error_message = "only the ops VM gets the Prometheus config"
  }
  assert {
    condition     = toset([for k in keys(aws_ssm_parameter.file) : k if startswith(k, "work-a/certs/")]) == toset(["work-a/certs/ca.crt", "work-a/certs/worker.crt", "work-a/certs/worker.key"])
    error_message = "a VM gets only its own roles' keys"
  }
  assert {
    condition     = keys(azurerm_traffic_manager_external_endpoint.gateway) == ["svc-a", "svc-z"]
    error_message = "both gateways are Traffic Manager endpoints"
  }
  assert {
    condition     = length(azurerm_network_security_group.vms[0].security_rule) == 2
    error_message = "the Azure gateway opens 443"
  }
}

run "act2_full" { # week 13: the database in both clouds and the witness
  command = apply
  variables {
    vms = {
      "svc-a"  = { cloud = "aws", mesh = 11, roles = ["gateway", "auth", "jobs", "dispatch", "scheduler"] }
      "work-a" = { cloud = "aws", mesh = 12, roles = ["worker"] }
      "pg-a"   = { cloud = "aws", mesh = 13, roles = ["etcd", "postgres"] }
      "ops-a"  = { cloud = "aws", mesh = 14, roles = ["ops"] }
      "svc-z"  = { cloud = "azure", mesh = 21, roles = ["gateway", "auth", "jobs", "dispatch", "scheduler"] }
      "work-z" = { cloud = "azure", mesh = 22, roles = ["worker"] }
      "pg-z"   = { cloud = "azure", mesh = 23, roles = ["etcd", "postgres"] }
      "wit"    = { cloud = "aws", mesh = 31, region = "ap-south-2", size = "t4g.micro", roles = ["etcd"] }
    }
  }

  assert {
    condition     = keys(module.hyderabad.ids) == ["wit"]
    error_message = "the witness lives in Hyderabad"
  }
  assert {
    condition     = strcontains(aws_ssm_parameter.file["pg-a/tq.env"].value, "ETCD_INITIAL_CLUSTER='pg-a=http://pg-a:2380,pg-z=http://pg-z:2380,wit=http://wit:2380'")
    error_message = "three etcd members, one per site"
  }
  assert {
    condition     = strcontains(jsondecode(azurerm_key_vault_secret.vm["svc-z"].value)["tq.env"], "AUTH_URL='https://svc-z:8083,https://svc-a:8083'")
    error_message = "each gateway calls its own cloud's auth"
  }
  assert {
    condition     = strcontains(jsondecode(azurerm_key_vault_secret.vm["svc-z"].value)["tq.env"], "@pg-a:5432,pg-z:5432/jobs?target_session_attrs=read-write")
    error_message = "services list both database nodes and find the primary"
  }
  assert {
    condition     = keys(azurerm_traffic_manager_external_endpoint.gateway) == ["svc-a", "svc-z"]
    error_message = "both gateways are Traffic Manager endpoints"
  }
  assert {
    condition     = length(azurerm_network_security_group.vms[0].security_rule) == 2
    error_message = "the Azure gateway opens 443"
  }
}
