package auth

import "time"

// SetNow replaces the handler's clock, so a test can move past a challenge's
// expiry without waiting for it.
func (h *Handler) SetNow(now func() time.Time) { h.now = now }
