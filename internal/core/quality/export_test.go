package quality

import "time"

// SetSchedulerClock lets a test say what time it is.
func SetSchedulerClock(s *Scheduler, now func() time.Time) { s.now = now }
