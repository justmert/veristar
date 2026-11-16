package config

// UmamiUserPasswordKey reads UMAMI_USER_PASSWORD_KEY: 32 bytes in standard
// base64. the control plane (member umami sessions, provisioning) and jobs (reconcile,
// umami-rotate) derive the same daily umami passwords from it.
func (e *Env) UmamiUserPasswordKey() Key32 { return e.key32("UMAMI_USER_PASSWORD_KEY") }
