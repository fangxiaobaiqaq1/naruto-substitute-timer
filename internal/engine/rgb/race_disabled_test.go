//go:build !race

package rgb

// raceEnabled bounds the slow cold-scan equivalence sequence under -race.
const raceEnabled = false
