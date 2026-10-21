//go:build !unix

package factory

import "time"

func processCPU() time.Duration { return 0 }
