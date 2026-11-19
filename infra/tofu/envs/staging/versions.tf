terraform {
  required_version = "= 1.13.9"

  required_providers {
    azurerm = {
      source  = "hashicorp/azurerm"
      version = "= 5.13.0"
    }
    cloudflare = {
      source  = "cloudflare/cloudflare"
      version = "5.29.0"
    }
  }

  # azure blob storage in the state account that make state-bootstrap
  # creates (versioned, zone-redundant, entra id only). the backend locks
  # the state with a blob lease and signs in with the az cli login.
  backend "azurerm" {
    subscription_id      = var.subscription_id
    resource_group_name  = var.state_resource_group
    storage_account_name = var.state_storage_account
    container_name       = "tfstate"
    key                  = "staging/tofu.tfstate"
    use_azuread_auth     = true
  }

  # state and plan files hold addresses and provider responses; enforced means
  # opentofu refuses to write either unencrypted
  encryption {
    key_provider "pbkdf2" "passphrase" {
      passphrase = var.state_passphrase
    }

    method "aes_gcm" "state" {
      keys = key_provider.pbkdf2.passphrase
    }

    state {
      method   = method.aes_gcm.state
      enforced = true
    }

    plan {
      method   = method.aes_gcm.state
      enforced = true
    }
  }
}

provider "azurerm" {
  subscription_id = var.subscription_id
  # the resource providers are registered once by the operator
  # (docs/deploy/prerequisites.md); tofu needs no subscription-wide rights
  resource_provider_registrations = "none"
  # the storage accounts take no account keys
  storage_use_azuread = true
  features {}
}

# reads CLOUDFLARE_API_TOKEN from the environment (the env's sops dotenv)
provider "cloudflare" {}
