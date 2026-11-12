locals {
  ip_range = "10.10.0.0/16"
  # production servers and addresses refuse deletion until this is flipped
  protect = true
}
