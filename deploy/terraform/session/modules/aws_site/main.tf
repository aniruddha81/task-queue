# One AWS region's VMs: a public subnet (no NAT gateway), firewall rules that admit only
# WireGuard from known peers (plus 443 on gateways), IMDSv2, and per-VM identities that can
# read only that VM's own parameters.

terraform {
  required_providers {
    aws = { source = "hashicorp/aws" }
  }
}

variable "vms" {
  type = map(object({ cloud = string, region = string, size = string, roles = list(string), mesh_ip = string }))
}
variable "cidr" { type = string }
variable "wg_sources" { type = list(string) }
variable "user_data" { type = map(string) }
variable "ssh_key" { type = string }

data "aws_region" "this" {}
data "aws_caller_identity" "me" {}

locals {
  on   = length(var.vms) > 0 ? 1 : 0
  arch = { for n, v in var.vms : n => contains(data.aws_ec2_instance_type.size[v.size].supported_architectures, "arm64") ? "arm64" : "amd64" }
}

data "aws_ec2_instance_type" "size" {
  for_each      = toset([for v in var.vms : v.size])
  instance_type = each.key
}

data "aws_ami" "ubuntu" {
  for_each    = toset(values(local.arch))
  most_recent = true
  owners      = ["099720109477"] # Canonical
  filter {
    name   = "name"
    values = ["ubuntu/images/hvm-ssd-gp3/ubuntu-noble-24.04-${each.key}-server-*"]
  }
}

resource "aws_vpc" "this" {
  count      = local.on
  cidr_block = var.cidr
  tags       = { Name = "tq" }
}

resource "aws_internet_gateway" "this" {
  count  = local.on
  vpc_id = aws_vpc.this[0].id
}

resource "aws_subnet" "this" {
  count             = local.on
  vpc_id            = aws_vpc.this[0].id
  cidr_block        = cidrsubnet(var.cidr, 8, 1)
  availability_zone = "${data.aws_region.this.region}a"
}

resource "aws_route_table" "this" {
  count  = local.on
  vpc_id = aws_vpc.this[0].id
  route {
    cidr_block = "0.0.0.0/0"
    gateway_id = aws_internet_gateway.this[0].id
  }
}

resource "aws_route_table_association" "this" {
  count          = local.on
  subnet_id      = aws_subnet.this[0].id
  route_table_id = aws_route_table.this[0].id
}

resource "aws_security_group" "base" {
  count  = local.on
  name   = "tq-base"
  vpc_id = aws_vpc.this[0].id
  ingress {
    description = "WireGuard from mesh peers, the admin laptop and this VPC"
    protocol    = "udp"
    from_port   = 51820
    to_port     = 51820
    cidr_blocks = concat(var.wg_sources, [var.cidr])
  }
  egress {
    protocol    = "-1"
    from_port   = 0
    to_port     = 0
    cidr_blocks = ["0.0.0.0/0"]
  }
}

resource "aws_security_group" "https" {
  count  = local.on
  name   = "tq-https"
  vpc_id = aws_vpc.this[0].id
  ingress {
    description = "The public gateway, its Traffic Manager probes and Lets Encrypt validation"
    protocol    = "tcp"
    from_port   = 443
    to_port     = 443
    cidr_blocks = ["0.0.0.0/0"]
  }
}

resource "aws_key_pair" "admin" {
  count      = var.ssh_key != "" ? local.on : 0
  key_name   = "tq-admin"
  public_key = var.ssh_key
}

resource "aws_iam_role" "vm" {
  for_each = var.vms
  name     = "tq-vm-${each.key}"
  assume_role_policy = jsonencode({
    Version   = "2012-10-17"
    Statement = [{ Effect = "Allow", Principal = { Service = "ec2.amazonaws.com" }, Action = "sts:AssumeRole" }]
  })
}

# Run Command (deploys) and Session Manager.
resource "aws_iam_role_policy_attachment" "ssm" {
  for_each   = var.vms
  role       = aws_iam_role.vm[each.key].name
  policy_arn = "arn:aws:iam::aws:policy/AmazonSSMManagedInstanceCore"
}

resource "aws_iam_role_policy" "files" {
  for_each = var.vms
  role     = aws_iam_role.vm[each.key].id
  policy = jsonencode({
    Version = "2012-10-17"
    Statement = [{
      Effect = "Allow"
      Action = ["ssm:GetParameter", "ssm:GetParametersByPath"]
      Resource = [for p in ["tq/vm/${each.key}", "tq/vm/${each.key}/*", "tq/release/*"] :
      "arn:aws:ssm:ap-south-1:${data.aws_caller_identity.me.account_id}:parameter/${p}"]
    }]
  })
}

resource "aws_iam_instance_profile" "vm" {
  for_each = var.vms
  name     = "tq-vm-${each.key}"
  role     = aws_iam_role.vm[each.key].name
}

resource "aws_instance" "vm" {
  for_each               = var.vms
  ami                    = data.aws_ami.ubuntu[local.arch[each.key]].id
  instance_type          = each.value.size
  subnet_id              = aws_subnet.this[0].id
  vpc_security_group_ids = concat([aws_security_group.base[0].id], contains(each.value.roles, "gateway") ? [aws_security_group.https[0].id] : [])
  iam_instance_profile   = aws_iam_instance_profile.vm[each.key].name
  key_name               = var.ssh_key != "" ? aws_key_pair.admin[0].key_name : null
  user_data              = var.user_data[each.key]
  metadata_options {
    http_endpoint = "enabled"
    http_tokens   = "required" # IMDSv2 only
  }
  credit_specification {
    cpu_credits = "standard" # never bill for burst beyond earned credits
  }
  root_block_device {
    volume_type = "gp3"
    volume_size = 16
    encrypted   = true
  }
  tags = { Name = each.key }
  lifecycle {
    ignore_changes = [ami] # a newer Ubuntu image must not replace running VMs
  }
}

# Elastic IPs exist apart from the instances, so firewall rules can name every peer's
# address without a dependency cycle.
resource "aws_eip" "vm" {
  for_each = var.vms
  domain   = "vpc"
  tags     = { Name = each.key }
}

resource "aws_eip_association" "vm" {
  for_each      = var.vms
  instance_id   = aws_instance.vm[each.key].id
  allocation_id = aws_eip.vm[each.key].id
}

output "ids" { value = { for n, i in aws_instance.vm : n => i.id } }
output "public_ips" { value = { for n, e in aws_eip.vm : n => e.public_ip } }
output "private_ips" { value = { for n, i in aws_instance.vm : n => i.private_ip } }
