package main

import (
	"os"
	"sync"
	"time"
)

// nowEnv freezes the clock at an RFC 3339 time, for the screenshots: ages,
// "stale" and --older-than then come out the same on every run.
const nowEnv = "WOMA_NOW"

var frozen = sync.OnceValue(func() *time.Time {
	t, err := time.Parse(time.RFC3339, os.Getenv(nowEnv))
	if err != nil {
		return nil
	}
	return &t
})

// now is the time woma shows ages and judges staleness by: WOMA_NOW's when
// it's set.
func now() time.Time {
	if t := frozen(); t != nil {
		return *t
	}
	return time.Now()
}
