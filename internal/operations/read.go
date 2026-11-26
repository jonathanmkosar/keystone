package operations

import (
	"context"
	"time"

	"github.com/jonathanmkosar/keystone/internal/schedules"
	"github.com/jonathanmkosar/keystone/internal/tenant"
	"github.com/jonathanmkosar/keystone/settings"
)

// Sources are what a report reads that a store alone does not give.
type Sources struct {
	// Schedules lists a person's schedules as the Scheduled screen does:
	// the scheduler's List. Nil shows none.
	Schedules func(ctx context.Context, s *tenant.Store) (schedules.SchedulesReply, error)
}

// Report is Operations and Monitor for one person, at one moment.
type Report struct {
	At        time.Time     `json:"at"`
	Jobs      JobsPart      `json:"jobs"`
	Schedules SchedulesPart `json:"schedules"`
	Models    ModelsPart    `json:"models"`
	Traces    []Trace       `json:"traces"`
	Limits    []Limit       `json:"limits"`
	// Crossed is how many limits are at or past their watch level.
	Crossed int `json:"crossed"`
}

// Read is a person's report at now: the one function behind GET
// /api/v1/operations.
func Read(ctx context.Context, s *tenant.Store, reg []settings.Setting, src Sources, now time.Time) (Report, error) {
	cfg, err := ReadSettings(ctx, s, reg)
	if err != nil {
		return Report{}, err
	}
	out := Report{At: now.UTC()}
	if out.Limits, err = limits(ctx, s, reg, cfg, now); err != nil {
		return Report{}, err
	}
	for _, l := range out.Limits {
		if l.Crossed {
			out.Crossed++
		}
	}
	if out.Jobs, err = readJobs(ctx, s, cfg, now); err != nil {
		return Report{}, err
	}
	if out.Schedules, err = readSchedules(ctx, s, src.Schedules); err != nil {
		return Report{}, err
	}
	if out.Models, err = readHealth(ctx, s, cfg, now); err != nil {
		return Report{}, err
	}
	if out.Traces, err = readTraces(ctx, s, cfg); err != nil {
		return Report{}, err
	}
	return out, nil
}

// WatchReply is the limits whose watch level is crossed: what the home
// screen shows a notice for.
type WatchReply struct {
	At time.Time `json:"at"`
	// Crossed are the limits at or past their watch level, in the order of
	// the report. Empty when none is.
	Crossed []Limit `json:"crossed"`
}

// Watch returns the limits whose watch level is crossed for a person at
// now. They are read by the same function as the report's, so the notice
// and the report never disagree.
func Watch(ctx context.Context, s *tenant.Store, reg []settings.Setting, now time.Time) (WatchReply, error) {
	all, err := Limits(ctx, s, reg, now)
	if err != nil {
		return WatchReply{}, err
	}
	out := WatchReply{At: now.UTC(), Crossed: []Limit{}}
	for _, l := range all {
		if l.Crossed {
			out.Crossed = append(out.Crossed, l)
		}
	}
	return out, nil
}
