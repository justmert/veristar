package config

// MinHourlyRetentionMonths is the floor of API_HOURLY_RETENTION_MONTHS. the
// rfp requires api aggregates for at least 24 months, so a lower value is
// refused at startup instead of quietly deleting data the rfp promises.
const MinHourlyRetentionMonths = 24

// APIRetention is how long the jobs retention task keeps api metric rows.
// both keys are required: the starting values (30 days, 36 months) live in
// deploy/compose and docs/reference/api-storage.md, so a deployment never
// runs with a horizon nobody chose.
type APIRetention struct {
	// MinuteDays is how many days of api.metrics_minute are kept.
	MinuteDays int
	// HourlyMonths is how many months of api.metrics_hourly are kept.
	HourlyMonths int
}

// LoadAPIRetention reads API_MINUTE_RETENTION_DAYS and
// API_HOURLY_RETENTION_MONTHS.
func LoadAPIRetention(e *Env) (APIRetention, error) {
	c := APIRetention{
		// at least 2 days, so the rollup of yesterday always finds its minutes
		MinuteDays:   e.Int("API_MINUTE_RETENTION_DAYS", 2, 3660),
		HourlyMonths: e.Int("API_HOURLY_RETENTION_MONTHS", MinHourlyRetentionMonths, 1200),
	}
	return c, e.Err()
}
