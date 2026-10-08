# Session stack: applied at the start of each cloud phase (make up), destroyed by make down.
# VMs come from the role map below; everything a VM needs (certificates, WireGuard config,
# passwords, peer addresses) is rendered here into per-VM files that the VM fetches at boot
# with its own identity: SSM Parameter Store on AWS, Key Vault on Azure. The VMs' boot
# config (cloud-init) holds no version and no secret, so a new commit never replaces a VM.

terraform {
  required_version = ">= 1.11"
  backend "s3" {
    key          = "session.tfstate"
    region       = "ap-south-1"
    encrypt      = true
    use_lockfile = true
    # bucket = tq-tfstate-<account id>, passed by make up
  }
  required_providers {
    aws       = { source = "hashicorp/aws", version = "~> 6.0" }
    azurerm   = { source = "hashicorp/azurerm", version = "~> 4.0" }
    tls       = { source = "hashicorp/tls", version = "~> 4.0" }
    random    = { source = "hashicorp/random", version = "~> 3.6" }
    wireguard = { source = "OJFord/wireguard", version = "~> 0.4" }
  }
}

variable "vms" {
  description = "Role map: which VMs exist and what runs on each. mesh = last octet of its 10.99.0.x mesh address (fixed, so adding a VM never renumbers the others)."
  type = map(object({
    cloud  = string
    mesh   = number
    roles  = list(string)
    region = optional(string)
    size   = optional(string)
  }))
  # Act 1 (v4 plan, placement table): one gateway and one database, the rest split.
  default = {
    "svc-a"  = { cloud = "aws", mesh = 11, roles = ["gateway", "jobs", "dispatch", "scheduler"] }
    "work-a" = { cloud = "aws", mesh = 12, roles = ["worker"] }
    "pg-a"   = { cloud = "aws", mesh = 13, roles = ["etcd", "postgres"] }
    "ops-a"  = { cloud = "aws", mesh = 14, roles = ["ops"] }
    "svc-z"  = { cloud = "azure", mesh = 21, roles = ["auth", "dispatch", "scheduler"] }
    "work-z" = { cloud = "azure", mesh = 22, roles = ["worker"] }
    # Act 2 (week 13) adds:
    # "pg-z" = { cloud = "azure", mesh = 23, roles = ["etcd", "postgres"] }
    # "wit"  = { cloud = "aws", mesh = 31, region = "ap-south-2", size = "t4g.micro", roles = ["etcd"] }
  }
  validation {
    condition = alltrue([for r in ["gateway", "auth", "jobs", "dispatch", "scheduler", "worker", "etcd", "postgres", "ops"] :
    anytrue([for v in var.vms : contains(v.roles, r)])])
    error_message = "Every role must run on at least one VM."
  }
  validation {
    condition     = alltrue([for v in var.vms : contains(["aws", "azure"], v.cloud) && v.mesh > 2 && v.mesh < 255])
    error_message = "cloud is aws or azure; mesh is 3-254 (10.99.0.2 is the admin laptop)."
  }
}

variable "admin_cidr" {
  description = "Where the admin laptop's WireGuard packets come from (make up passes your current IP)."
  default     = ""
}

variable "admin_wg_public_key" {
  description = "The admin laptop's WireGuard public key (its private key never leaves the laptop). Empty: no admin peer."
  default     = ""
}

variable "admin_ssh_public_key" {
  description = "SSH key for admin access over the mesh (port 22 is never public). Empty: none on AWS, a discarded one on Azure."
  default     = ""
}

variable "github_repo" {
  default = "aniruddha81/task-queue"
}

provider "aws" {
  region = "ap-south-1"
  default_tags { tags = { project = "tq", stack = "session" } }
}

provider "aws" {
  alias  = "hyd"
  region = "ap-south-2"
  default_tags { tags = { project = "tq", stack = "session" } }
}

provider "azurerm" {
  features {
    key_vault {
      purge_soft_deleted_secrets_on_destroy = true # the next make up reuses the names
    }
  }
}

data "aws_caller_identity" "me" {}

data "terraform_remote_state" "persistent" {
  backend = "s3"
  config = {
    bucket = "tq-tfstate-${data.aws_caller_identity.me.account_id}"
    key    = "persistent.tfstate"
    region = "ap-south-1"
  }
}

locals {
  p = data.terraform_remote_state.persistent.outputs

  site = {
    aws   = { region = "ap-south-1", size = "t4g.small" }           # free-plan eligible
    azure = { region = "centralindia", size = "Standard_B2ats_v2" } # Bsv2 quota; plain B is too small
  }
  vms = { for n, v in var.vms : n => {
    cloud   = v.cloud
    region  = coalesce(v.region, local.site[v.cloud].region)
    size    = coalesce(v.size, local.site[v.cloud].size)
    roles   = v.roles
    mesh_ip = cidrhost("10.99.0.0/24", v.mesh)
  } }
  names = sort(keys(local.vms))
  roles = ["gateway", "auth", "jobs", "dispatch", "scheduler", "worker", "etcd", "postgres", "ops"]

  public_ip  = merge(module.mumbai.public_ips, module.hyderabad.public_ips, { for n, ip in azurerm_public_ip.vm : n => ip.ip_address })
  private_ip = merge(module.mumbai.private_ips, module.hyderabad.private_ips, { for n, nic in azurerm_network_interface.vm : n => nic.private_ip_address })

  # Where WireGuard packets may come from: every VM, plus the admin laptop.
  wg_sources = concat([for n in local.names : "${local.public_ip[n]}/32"], var.admin_cidr == "" ? [] : [var.admin_cidr])

  # For each VM and role, the VMs running that role, nearest (same cloud) first.
  near = { for n, v in local.vms : n => { for r in local.roles : r => concat(
    [for m in local.names : m if contains(local.vms[m].roles, r) && local.vms[m].cloud == v.cloud],
    [for m in local.names : m if contains(local.vms[m].roles, r) && local.vms[m].cloud != v.cloud],
  ) } }
  ops_vm   = [for m in local.names : m if contains(local.vms[m].roles, "ops")][0]
  etcd_vms = [for m in local.names : m if contains(local.vms[m].roles, "etcd")]
  pg_hosts = join(",", [for m in local.names : "${m}:5432" if contains(local.vms[m].roles, "postgres")])
  db_url = { for db in ["jobs", "auth"] :
    db => "postgres://${db}_svc:${random_password.pw["${db}_db"].result}@${local.pg_hosts}/${db}?target_session_attrs=read-write&sslmode=verify-full&sslrootcert=/certs/ca.crt"
  }

  # Each VM's settings, for deploy/vm/<role>.compose.yml. Secrets only go to VMs whose roles use them.
  env = { for n, v in local.vms : n => merge(
    { VM_NAME = n, CLOUD = v.cloud, MESH_IP = v.mesh_ip, ROLES = join(" ", v.roles), IMAGE = "ghcr.io/${var.github_repo}" },
    contains(v.roles, "gateway") ? {
      AUTH_URL      = "https://${local.near[n].auth[0]}:8083"
      JOBS_URL      = "https://${local.near[n].jobs[0]}:8080"
      PUBLIC_ORIGIN = "https://${local.p.public_fqdn}"
      ACME_DOMAIN   = local.p.public_fqdn
    } : {},
    contains(v.roles, "auth") ? {
      AUTH_DB_URL     = local.db_url.auth
      JWT_PRIVATE_KEY = random_bytes.jwt.base64
      SEED_USERS      = join(",", [for u, role in { admin = ":admin", demo = "", smoke = "" } : "${u}@example.com:${random_password.pw["user_${u}"].result}${role}"])
    } : {},
    contains(v.roles, "jobs") ? { JOBS_DB_URL = local.db_url.jobs, AUTH_URL = "https://${local.near[n].auth[0]}:8083" } : {},
    contains(v.roles, "dispatch") || contains(v.roles, "scheduler") ? { JOBS_DB_URL = local.db_url.jobs } : {},
    contains(v.roles, "worker") ? {
      DISPATCHERS   = join(",", [for m in local.near[n].dispatch : "https://${m}:8081"])
      SINKS_URL     = "https://${local.ops_vm}:8090"
      SMTP_ADDR     = "${local.ops_vm}:1025"
      WEBHOOK_ALLOW = "https://${local.ops_vm}:8090/webhook"
    } : {},
    contains(v.roles, "etcd") ? { ETCD_INITIAL_CLUSTER = join(",", [for m in local.etcd_vms : "${m}=http://${m}:2380"]) } : {},
    contains(v.roles, "postgres") ? {
      ETCD_HOSTS                   = join(",", [for m in local.etcd_vms : "${m}:2379"])
      PATRONI_SUPERUSER_PASSWORD   = random_password.pw["superuser"].result
      PATRONI_REPLICATION_PASSWORD = random_password.pw["replication"].result
      JOBS_DB_PASSWORD             = random_password.pw["jobs_db"].result
      AUTH_DB_PASSWORD             = random_password.pw["auth_db"].result
    } : {},
    contains(v.roles, "ops") ? {
      JOBS_DB_URL       = local.db_url.jobs # migrations run here
      AUTH_DB_URL       = local.db_url.auth
      SINKS_DB_PASSWORD = random_password.pw["sinks_db"].result
      SINKS_DB_URL      = "postgres://postgres:${random_password.pw["sinks_db"].result}@localhost:5433/sinks?sslmode=disable"
      HARNESS_PASSWORD  = random_password.pw["user_demo"].result # the chaos harness submits as demo@example.com
    } : {},
  ) }

  hosts = join("", concat(["# tq-begin\n"], [for m in local.names : "${local.vms[m].mesh_ip} ${m}\n"], ["# tq-end\n"]))

  wg = { for n, v in local.vms : n => templatefile("${path.module}/wg0.conf.tftpl", {
    ip        = v.mesh_ip
    key       = wireguard_asymmetric_key.vm[n].private_key
    admin_key = var.admin_wg_public_key
    peers = [for m in local.names : {
      name = m
      key  = wireguard_asymmetric_key.vm[m].public_key
      ip   = local.vms[m].mesh_ip
      # Same cloud and region: the private network. Otherwise across the internet.
      endpoint = local.vms[m].cloud == v.cloud && local.vms[m].region == v.region ? local.private_ip[m] : local.public_ip[m]
    } if m != n]
  }) }

  prometheus = templatefile("${path.module}/prometheus.yml.tftpl", {
    jobs = { for job, port in { dispatch = 8081, scheduler = 8082 } : job => [
      for m in local.names : { target = "${m}:${port}", vm = m, cloud = local.vms[m].cloud } if contains(local.vms[m].roles, job)
    ] }
  })

  # Certificates each role's containers present (mTLS everywhere).
  role_certs = {
    gateway = ["gateway"], auth = ["auth"], jobs = ["jobs"], dispatch = ["dispatch"], scheduler = ["scheduler"],
    worker  = ["worker"], etcd = [], postgres = ["postgres"], ops = ["sinks", "prometheus", "migrate"]
  }
  vm_certs = { for n, v in local.vms : n => distinct(flatten([for r in v.roles : local.role_certs[r]])) }

  # Everything a VM fetches at boot, as {path under /etc/tq: content}.
  files = { for n, v in local.vms : n => merge(
    {
      "tq.env"       = join("", [for k in sort(keys(local.env[n])) : "${k}='${local.env[n][k]}'\n"])
      "hosts"        = local.hosts
      "wg0.conf"     = local.wg[n]
      "certs/ca.crt" = tls_self_signed_cert.ca.cert_pem
    },
    { for c in local.vm_certs[n] : "certs/${c}.crt" => tls_locally_signed_cert.svc[c].cert_pem },
    { for c in local.vm_certs[n] : "certs/${c}.key" => tls_private_key.svc[c].private_key_pem },
    contains(v.roles, "ops") ? { "prometheus.yml" = local.prometheus } : {},
  ) }

  cloud_init = { for n, v in local.vms : n => templatefile("${path.module}/cloud-init.yaml.tftpl", {
    name       = n
    cloud      = v.cloud
    repo       = var.github_repo
    ssm_region = "ap-south-1"
    vault      = local.p.key_vault_name
    converge   = file("${path.module}/tq-converge.sh")
  }) }

  # Rollout order: data first (then migrations), the ops VM, auth before its callers, workers last.
  stage_rank  = { etcd = 0, postgres = 0, ops = 1, auth = 2, gateway = 3, jobs = 3, dispatch = 3, scheduler = 3, worker = 4 }
  rank        = { for n, v in local.vms : n => min([for r in v.roles : local.stage_rank[r]]...) }
  order       = flatten([for k in range(5) : [for n in local.names : n if local.rank[n] == k]])
  instance_id = merge(module.mumbai.ids, module.hyderabad.ids)
}

# ---------- Secrets ----------

resource "random_password" "pw" {
  for_each = toset(["jobs_db", "auth_db", "sinks_db", "superuser", "replication", "user_admin", "user_demo", "user_smoke"])
  length   = 24
  special  = false # they go into URLs and SEED_USERS unescaped
}

resource "random_bytes" "jwt" {
  length = 32 # Ed25519 seed
}

resource "wireguard_asymmetric_key" "vm" {
  for_each = local.vms
}

# The project CA: every internal call is mTLS with these certificates.
resource "tls_private_key" "ca" {
  algorithm   = "ECDSA"
  ecdsa_curve = "P256"
}

resource "tls_self_signed_cert" "ca" {
  private_key_pem = tls_private_key.ca.private_key_pem
  subject { common_name = "task-queue cloud CA" }
  is_ca_certificate     = true
  validity_period_hours = 24 * 365
  allowed_uses          = ["cert_signing", "crl_signing", "digital_signature"]
}

resource "tls_private_key" "svc" {
  for_each    = toset(flatten(values(local.role_certs)))
  algorithm   = "ECDSA"
  ecdsa_curve = "P256"
}

resource "tls_cert_request" "svc" {
  for_each        = tls_private_key.svc
  private_key_pem = each.value.private_key_pem
  subject { common_name = each.key }
  # Valid on every VM: services are addressed by VM name over the mesh.
  dns_names    = concat([each.key, "localhost"], local.names)
  ip_addresses = ["127.0.0.1"]
}

resource "tls_locally_signed_cert" "svc" {
  for_each              = tls_cert_request.svc
  cert_request_pem      = each.value.cert_request_pem
  ca_private_key_pem    = tls_private_key.ca.private_key_pem
  ca_cert_pem           = tls_self_signed_cert.ca.cert_pem
  validity_period_hours = 24 * 365
  allowed_uses          = ["digital_signature", "key_encipherment", "server_auth", "client_auth"]
}

# AWS VMs read /tq/vm/<name>/* (one parameter per file); Azure VMs read one Key Vault
# secret each (azure.tf). All parameters live in Mumbai, whatever region the VM is in.
resource "aws_ssm_parameter" "file" {
  for_each = merge([for n, v in local.vms : { for f, c in local.files[n] : "${n}/${f}" => c } if v.cloud == "aws"]...)
  name     = "/tq/vm/${each.key}"
  type     = "SecureString"
  tier     = "Intelligent-Tiering" # standard (free) unless a file outgrows 4 KB
  value    = each.value
}

# What rollout.sh needs: the public name, the vault, and the VMs in rollout order.
resource "aws_ssm_parameter" "topology" {
  name = "/tq/release/topology"
  type = "String"
  value = jsonencode({
    fqdn  = local.p.public_fqdn
    vault = local.p.key_vault_name
    ops   = local.ops_vm
    vms = [for n in local.order : {
      name   = n
      cloud  = local.vms[n].cloud
      region = local.vms[n].region
      stage  = local.rank[n] == 0 ? "data" : "app"
      roles  = local.vms[n].roles
      id     = local.vms[n].cloud == "aws" ? local.instance_id[n] : n
      rg     = local.p.session_resource_group
    }]
  })
}

resource "aws_ssm_parameter" "smoke_password" {
  name  = "/tq/ci/smoke-password"
  type  = "SecureString"
  value = random_password.pw["user_smoke"].result
}

# ---------- Outputs ----------

output "public_url" {
  value = "https://${local.p.public_fqdn}"
}

output "vms" {
  value = { for n in local.order : n => { cloud = local.vms[n].cloud, public_ip = local.public_ip[n], mesh_ip = local.vms[n].mesh_ip, roles = join(" ", local.vms[n].roles) } }
}

output "admin_wg_conf" {
  description = "WireGuard config for the admin laptop: add your [Interface] PrivateKey, then wg-quick up."
  value = join("", concat(
    ["[Interface]\nAddress = 10.99.0.2/24\nPrivateKey = <your laptop's private key>\nMTU = 1380\n"],
    [for m in local.names : "\n[Peer]\n# ${m}\nPublicKey = ${wireguard_asymmetric_key.vm[m].public_key}\nAllowedIPs = ${local.vms[m].mesh_ip}/32\nEndpoint = ${local.public_ip[m]}:51820\nPersistentKeepalive = 25\n"],
  ))
}

output "users" {
  description = "Seeded logins (registration is off in the cloud)."
  sensitive   = true
  value       = { for u in ["admin", "demo", "smoke"] : "${u}@example.com" => random_password.pw["user_${u}"].result }
}
