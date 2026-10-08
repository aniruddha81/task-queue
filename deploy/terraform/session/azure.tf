# Azure VMs (Central India), in the tq-session resource group the persistent stack owns.

locals {
  azure_vms   = { for n, v in local.vms : n => v if v.cloud == "azure" }
  azure_on    = length(local.azure_vms) > 0 ? 1 : 0
  azure_https = anytrue([for v in values(local.azure_vms) : contains(v.roles, "gateway")])
  rg          = local.p.session_resource_group
}

resource "azurerm_virtual_network" "tq" {
  count               = local.azure_on
  name                = "tq"
  location            = local.site.azure.region
  resource_group_name = local.rg
  address_space       = ["10.20.0.0/16"]
}

resource "azurerm_subnet" "vms" {
  count                = local.azure_on
  name                 = "vms"
  resource_group_name  = local.rg
  virtual_network_name = azurerm_virtual_network.tq[0].name
  address_prefixes     = ["10.20.1.0/24"]
}

resource "azurerm_network_security_group" "vms" {
  count               = local.azure_on
  name                = "tq-vms"
  location            = local.site.azure.region
  resource_group_name = local.rg
  security_rule {
    name                       = "wireguard"
    description                = "WireGuard from mesh peers and the admin laptop (the VNet is allowed by default)"
    priority                   = 100
    direction                  = "Inbound"
    access                     = "Allow"
    protocol                   = "Udp"
    source_address_prefixes    = local.wg_sources
    source_port_range          = "*"
    destination_address_prefix = "*"
    destination_port_range     = "51820"
  }
  dynamic "security_rule" {
    for_each = local.azure_https ? [1] : []
    content {
      name                       = "https"
      description                = "The public gateway; nothing else listens on 443"
      priority                   = 110
      direction                  = "Inbound"
      access                     = "Allow"
      protocol                   = "Tcp"
      source_address_prefix      = "Internet"
      source_port_range          = "*"
      destination_address_prefix = "*"
      destination_port_range     = "443"
    }
  }
}

resource "azurerm_public_ip" "vm" {
  for_each            = local.azure_vms
  name                = each.key
  location            = each.value.region
  resource_group_name = local.rg
  allocation_method   = "Static"
  sku                 = "Standard"
}

resource "azurerm_network_interface" "vm" {
  for_each            = local.azure_vms
  name                = each.key
  location            = each.value.region
  resource_group_name = local.rg
  ip_configuration {
    name                          = "ip"
    subnet_id                     = azurerm_subnet.vms[0].id
    private_ip_address_allocation = "Dynamic"
    public_ip_address_id          = azurerm_public_ip.vm[each.key].id
  }
}

resource "azurerm_network_interface_security_group_association" "vm" {
  for_each                  = local.azure_vms
  network_interface_id      = azurerm_network_interface.vm[each.key].id
  network_security_group_id = azurerm_network_security_group.vms[0].id
}

# Azure insists on an admin key. Without one from you, this one is generated and never
# used: port 22 is closed to the internet, and admin access goes over the mesh.
resource "tls_private_key" "azure_admin" {
  algorithm = "RSA"
  rsa_bits  = 4096
}

resource "azurerm_linux_virtual_machine" "vm" {
  for_each              = local.azure_vms
  name                  = each.key
  computer_name         = each.key
  location              = each.value.region
  resource_group_name   = local.rg
  size                  = each.value.size
  admin_username        = "tq"
  network_interface_ids = [azurerm_network_interface.vm[each.key].id]
  custom_data           = base64encode(local.cloud_init[each.key])
  admin_ssh_key {
    username   = "tq"
    public_key = var.admin_ssh_public_key != "" ? var.admin_ssh_public_key : tls_private_key.azure_admin.public_key_openssh
  }
  os_disk {
    caching              = "ReadWrite"
    storage_account_type = "StandardSSD_LRS" # Standard HDD OS disks retire in September 2028
    disk_size_gb         = 30
  }
  source_image_reference {
    publisher = "Canonical"
    offer     = "ubuntu-24_04-lts"
    sku       = "server"
    version   = "latest"
  }
  identity {
    type = "SystemAssigned"
  }
}

# One secret per VM holding all its files as JSON; the VM's identity can read only that
# secret and the current-release record.
resource "azurerm_key_vault_secret" "vm" {
  for_each     = local.azure_vms
  name         = "vm-${each.key}"
  value        = jsonencode(local.files[each.key])
  content_type = "application/json"
  key_vault_id = local.p.key_vault_id
}

resource "azurerm_role_assignment" "vm_files" {
  for_each             = local.azure_vms
  scope                = azurerm_key_vault_secret.vm[each.key].resource_versionless_id
  role_definition_name = "Key Vault Secrets User"
  principal_id         = azurerm_linux_virtual_machine.vm[each.key].identity[0].principal_id
}

resource "azurerm_role_assignment" "vm_release" {
  for_each             = local.azure_vms
  scope                = local.p.release_current_secret_id
  role_definition_name = "Key Vault Secrets User"
  principal_id         = azurerm_linux_virtual_machine.vm[each.key].identity[0].principal_id
}

# Every gateway, in either cloud, is a Traffic Manager endpoint.
resource "azurerm_traffic_manager_external_endpoint" "gateway" {
  for_each   = { for n, v in local.vms : n => v if contains(v.roles, "gateway") }
  name       = each.key
  profile_id = local.p.traffic_manager_profile_id
  target     = local.public_ip[each.key]
  lifecycle {
    ignore_changes = [enabled] # a deploy drains a gateway by disabling its endpoint
  }
}
