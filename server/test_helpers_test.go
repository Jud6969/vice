// server/test_helpers_test.go
// Copyright(c) 2022-2026 vice contributors, licensed under the GNU Public License, Version 3.
// SPDX: GPL-3.0-only

package server

import (
	"io"
	"log/slog"
	"testing"

	"github.com/mmp/vice/log"
	"github.com/mmp/vice/sim"
)

// newTestManagerWithHuman builds a SimManager with one signed-on human at
// a fresh TCW. Returns the manager, that human's controller token, and
// the TCW. The manager is constructed by hand (skipping NewSimManager) so
// no HTTP server, WX provider, or background update loop is launched.
func newTestManagerWithHuman(t *testing.T) (*SimManager, string, sim.TCW) {
	t.Helper()
	lg := &log.Logger{Logger: slog.New(slog.NewTextHandler(io.Discard, nil))}
	sm := &SimManager{
		sessionsByName:  make(map[string]*simSession),
		sessionsByToken: make(map[string]*simSession),
		lg:              lg,
	}

	s := sim.NewTestSim(lg)
	tcw := sim.E2ETCW()
	_, eventSub, err := s.SignOn(tcw, nil)
	if err != nil {
		t.Fatalf("sim.SignOn: %v", err)
	}

	session := makeLocalSimSession(s, lg)
	sm.sessionsByName[session.name] = session

	token := sm.makeControllerToken()
	session.AddHumanController(token, tcw, "AA", eventSub)
	sm.sessionsByToken[token] = session

	return sm, token, tcw
}

// addReliefHuman attaches a second human to the same TCW under relief
// semantics: no second sim.SignOn (the position is already signed in),
// just a fresh token + event subscription.
func addReliefHuman(t *testing.T, sm *SimManager, tcw sim.TCW) string {
	t.Helper()
	var session *simSession
	for _, ss := range sm.sessionsByName {
		session = ss
		break
	}
	if session == nil {
		t.Fatalf("addReliefHuman: no session in manager")
	}

	token := sm.makeControllerToken()
	eventSub := session.sim.Subscribe()
	session.AddHumanController(token, tcw, "BB", eventSub)
	sm.sessionsByToken[token] = session
	return token
}

// newHumanAt signs a fresh human in at the given TCW after everyone
// previously at it has signed off. Mirrors the ConnectToSim non-relief
// path: calls sim.SignOn, which re-uses the persisted TCWDisplay.
func newHumanAt(t *testing.T, sm *SimManager, tcw sim.TCW) string {
	t.Helper()
	var session *simSession
	for _, ss := range sm.sessionsByName {
		session = ss
		break
	}
	if session == nil {
		t.Fatalf("newHumanAt: no session in manager")
	}

	_, eventSub, err := session.sim.SignOn(tcw, nil)
	if err != nil {
		t.Fatalf("sim.SignOn: %v", err)
	}

	token := sm.makeControllerToken()
	session.AddHumanController(token, tcw, "CC", eventSub)
	sm.sessionsByToken[token] = session
	return token
}
