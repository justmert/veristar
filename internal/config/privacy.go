package config

import "time"

// DefaultErasureQueueWait is how long an erasure run waits for the replayer
// to take every event of the site that was queued before the request. a
// healthy replayer drains a site in seconds; a longer wait means umami or the
// replayer is behind, and the run fails without deleting anything.
const DefaultErasureQueueWait = 15 * time.Minute

// Erasure holds the optional erasure settings of the control plane
// (control-plane-api section 10.6).
type Erasure struct {
	QueueWait time.Duration
}

// LoadErasure reads ERASURE_QUEUE_WAIT, DefaultErasureQueueWait when unset.
func LoadErasure(e *Env) (Erasure, error) {
	c := Erasure{QueueWait: DefaultErasureQueueWait}
	if e.Has("ERASURE_QUEUE_WAIT") {
		c.QueueWait = e.Duration("ERASURE_QUEUE_WAIT")
		if c.QueueWait > 72*time.Hour {
			e.fail("ERASURE_QUEUE_WAIT", "must be at most 72h, the max age of WEB_EVENTS")
		}
	}
	return c, e.Err()
}

// DefaultWebRawRetentionMonths keeps raw umami rows for 25 months, so tenant
// dashboards always cover 24 full months.
const DefaultWebRawRetentionMonths = 25

// MinWebRawRetentionMonths is the floor of WEB_RAW_RETENTION_MONTHS. tenant
// dashboards read umami's raw rows and must cover 24 full months; the month
// in progress is the 25th. a lower value is a startup error, never clamped,
// like API_HOURLY_RETENTION_MONTHS.
const MinWebRawRetentionMonths = 25

// WebRetention is the setting of `jobs retain-web`.
type WebRetention struct {
	Months int
}

// LoadWebRetention reads WEB_RAW_RETENTION_MONTHS, DefaultWebRawRetentionMonths
// when unset, MinWebRawRetentionMonths to 1200 when set.
func LoadWebRetention(e *Env) (WebRetention, error) {
	c := WebRetention{Months: DefaultWebRawRetentionMonths}
	if e.Has("WEB_RAW_RETENTION_MONTHS") {
		c.Months = e.Int("WEB_RAW_RETENTION_MONTHS", MinWebRawRetentionMonths, 1200)
	}
	return c, e.Err()
}
