terraform {
  required_version = "= 1.13.9"

  required_providers {
    hcloud = {
      source  = "hetznercloud/hcloud"
      version = "= 1.70.2"
    }
    bunnynet = {
      source  = "BunnyWay/bunnynet"
      version = "= 0.20.1"
    }
    scaleway = {
      source  = "scaleway/scaleway"
      version = "= 2.91.0"
    }
  }

  # hetzner object storage through the s3 backend. credentials come from
  # AWS_ACCESS_KEY_ID and AWS_SECRET_ACCESS_KEY (sops). the bucket is
  # unversioned because hetzner does not support conditional puts on versioned
  # buckets, and use_lockfile needs them.
  backend "s3" {
    bucket = var.state_bucket
    key    = "staging/tofu.tfstate"
    region = var.state_location
    endpoints = {
      s3 = "https://${var.state_location}.your-objectstorage.com"
    }
    use_lockfile                = true
    skip_credentials_validation = true
    skip_region_validation      = true
    skip_requesting_account_id  = true
    skip_metadata_api_check     = true
    skip_s3_checksum            = true
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
