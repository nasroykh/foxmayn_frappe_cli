//go:build race

package store

// raceEnabled is true under -race, where timing assertions mean nothing.
const raceEnabled = true
