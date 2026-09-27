package devices

import "time"

// nowFn returns the current time. It is a package-level variable so
// tests can substitute a deterministic clock without changing the
// repository's signature.
var nowFn = time.Now
