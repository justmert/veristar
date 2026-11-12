# hetzner has no api for object storage buckets or s3 credentials (hcloud
# 1.70.2 guide s3-object-storage.md). keys are made once in the hetzner console
# an operator input and buckets through the s3 api, so this module only derives the
# values the other tiers need.

variable "location" {
  description = "object storage location"
  type        = string

  validation {
    condition     = contains(["fsn1", "nbg1", "hel1"], var.location)
    error_message = "location must be fsn1, nbg1 or hel1."
  }
}

variable "backup_bucket" {
  description = "backup bucket name, unique across hetzner object storage"
  type        = string

  validation {
    condition     = can(regex("^[a-z0-9][a-z0-9-]{1,61}[a-z0-9]$", var.backup_bucket))
    error_message = "bucket names are 3 to 63 lowercase letters, digits and hyphens."
  }
}

output "settings" {
  value = {
    endpoint      = "https://${var.location}.your-objectstorage.com"
    region        = var.location
    backup_bucket = var.backup_bucket
  }
}
