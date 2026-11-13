terraform {
  required_providers {
    azurerm = {
      source = "hashicorp/azurerm"
    }
  }
}

variable "name" {
  type = string
}

variable "tier" {
  type = string

  validation {
    condition     = contains(["edge", "core", "restore"], var.tier)
    error_message = "tier must be edge, core or restore."
  }
}

variable "index" {
  type = number
}

variable "resource_group_name" {
  type = string
}

variable "location" {
  description = "azure region"
  type        = string
}

variable "zone" {
  description = "availability zone, 1 to 3"
  type        = string

  validation {
    condition     = contains(["1", "2", "3"], var.zone)
    error_message = "zone must be 1, 2 or 3."
  }
}

variable "size" {
  description = "vm size"
  type        = string
}

variable "image" {
  description = "marketplace image reference"
  type = object({
    publisher = string
    offer     = string
    sku       = string
    version   = string
  })
}

variable "os_disk_gb" {
  type = number
}

variable "os_disk_type" {
  description = "managed disk type of the os disk"
  type        = string
}

variable "subnet_id" {
  type = string
}

variable "private_ip" {
  type = string
}

variable "identity_ids" {
  description = "user-assigned managed identities attached to the vm; empty for none"
  type        = list(string)
  default     = []
}

variable "admin_user" {
  type = string
}

variable "admin_ssh_public_keys" {
  type = list(string)
}

variable "protect" {
  description = "delete locks on the vm and its public address"
  type        = bool
}

variable "tags" {
  type = map(string)
}

locals {
  tags = merge(var.tags, { tier = var.tier })

  # cloud-init only creates the admin user; everything else is ansible, so a
  # converge can be rerun without touching the server. the list replaces
  # the image's default user, so the admin user is the only login.
  user_data = "#cloud-config\n${yamlencode({
    users = [{
      name                = var.admin_user
      groups              = ["sudo"]
      shell               = "/bin/bash"
      sudo                = "ALL=(ALL) NOPASSWD:ALL"
      lock_passwd         = true
      ssh_authorized_keys = var.admin_ssh_public_keys
    }]
    ssh_pwauth   = false
    disable_root = true
  })}"
}

# a separate static address keeps the public ip when the vm is rebuilt, so
# the traffic manager endpoints and allowlists elsewhere stay valid
resource "azurerm_public_ip" "v4" {
  name                = "${var.name}-v4"
  resource_group_name = var.resource_group_name
  location            = var.location
  zones               = [var.zone]
  sku                 = "Standard"
  allocation_method   = "Static"
  ip_version          = "IPv4"
  tags                = local.tags
}

resource "azurerm_network_interface" "this" {
  name                = "${var.name}-nic"
  resource_group_name = var.resource_group_name
  location            = var.location
  tags                = local.tags

  ip_configuration {
    name                          = "primary"
    primary                       = true
    subnet_id                     = var.subnet_id
    private_ip_address_allocation = "Static"
    private_ip_address            = var.private_ip
    public_ip_address_id          = azurerm_public_ip.v4.id
  }
}

resource "azurerm_linux_virtual_machine" "this" {
  name                  = var.name
  computer_name         = var.name
  resource_group_name   = var.resource_group_name
  location              = var.location
  zone                  = var.zone
  size                  = var.size
  network_interface_ids = [azurerm_network_interface.this.id]
  admin_username        = var.admin_user
  # no password exists; ssh keys only
  disable_password_authentication = true
  custom_data                     = base64encode(local.user_data)
  # no vm extensions are used; ansible does all configuration
  allow_extension_operations = false
  tags                       = local.tags

  dynamic "admin_ssh_key" {
    for_each = toset(var.admin_ssh_public_keys)
    content {
      username   = var.admin_user
      public_key = admin_ssh_key.value
    }
  }

  dynamic "identity" {
    for_each = length(var.identity_ids) > 0 ? [1] : []
    content {
      type         = "UserAssigned"
      identity_ids = var.identity_ids
    }
  }

  os_disk {
    name                 = "${var.name}-os"
    caching              = "ReadWrite"
    storage_account_type = var.os_disk_type
    disk_size_gb         = var.os_disk_gb
  }

  source_image_reference {
    publisher = var.image.publisher
    offer     = var.image.offer
    sku       = var.image.sku
    version   = var.image.version
  }

  lifecycle {
    # key rotation happens through ansible, and a newer image is a rebuild
    # the operator chooses; neither may replace a node on a plain apply
    ignore_changes = [admin_ssh_key, custom_data, source_image_reference]
  }
}

resource "azurerm_management_lock" "vm" {
  count = var.protect ? 1 : 0

  name       = "${var.name}-no-delete"
  scope      = azurerm_linux_virtual_machine.this.id
  lock_level = "CanNotDelete"
  notes      = "managed by opentofu; set protect = false in envs/<env>/env.tf to delete"
}

resource "azurerm_management_lock" "ip" {
  count = var.protect ? 1 : 0

  name       = "${var.name}-v4-no-delete"
  scope      = azurerm_public_ip.v4.id
  lock_level = "CanNotDelete"
  notes      = "managed by opentofu; set protect = false in envs/<env>/env.tf to delete"
}

output "name" {
  value = azurerm_linux_virtual_machine.this.name
}

output "public_ipv4" {
  value = azurerm_public_ip.v4.ip_address
}

output "public_ipv6" {
  description = "the nodes are ipv4 only"
  value       = null
}

output "private_ip" {
  value = var.private_ip
}

output "server_type" {
  value = azurerm_linux_virtual_machine.this.size
}

output "zone" {
  value = var.zone
}
