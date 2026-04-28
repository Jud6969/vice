// client/voice.go
// Copyright(c) 2026 vice contributors, licensed under the GNU Public License, Version 3.
// SPDX: GPL-3.0-only

package client

import (
	"sync"

	"github.com/mmp/vice/log"
	"github.com/mmp/vice/server"
)

// PTTRelay coordinates a single controller's PTT-press lifecycle with the
// server: it asks the server for the talker slot on press, forwards mic
// chunks while granted, and releases the slot on release. When denied, the
// caller is responsible for playing the heterodyne tone — PTTRelay just
// reports state.
//
// All public methods are safe to call from the UI goroutine.
type PTTRelay struct {
	mu      sync.Mutex
	client  *RPCClient
	token   string
	pressed bool // PTT key is currently held
	granted bool // server granted us the talker slot for this press
	denied  bool // server denied this press; caller has played the tone
	lg      *log.Logger
}

// NewPTTRelay wires up a relay for a single client connection.
func NewPTTRelay(client *RPCClient, controllerToken string, lg *log.Logger) *PTTRelay {
	return &PTTRelay{
		client: client,
		token:  controllerToken,
		lg:     lg,
	}
}

// Press is called when the PTT key transitions from up to down.
// Returns:
//   - granted=true if the server gave us the talker slot. Caller should
//     start mic capture and feed chunks via SendChunk.
//   - granted=false if the server denied us. Caller should play the
//     heterodyne tone and skip mic capture for this press. SendChunk and
//     Release will be no-ops until the next Press.
func (r *PTTRelay) Press() (granted bool) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.pressed {
		// Defensive: avoid double-press.
		return r.granted
	}
	r.pressed = true

	var reply server.StartPTTReply
	if err := r.client.callWithTimeout(server.StartPTTRPC, r.token, &reply); err != nil {
		r.lg.Errorf("StartPTT RPC failed: %v", err)
		r.denied = true
		return false
	}
	if reply.Granted {
		r.granted = true
		return true
	}
	r.denied = true
	return false
}

// SendChunk forwards a 20 ms PCM chunk to the server. No-op when the
// current press is denied or the relay is not pressed.
func (r *PTTRelay) SendChunk(samples []int16) {
	r.mu.Lock()
	if !r.pressed || !r.granted {
		r.mu.Unlock()
		return
	}
	token := r.token
	r.mu.Unlock()

	args := &server.StreamPTTAudioArgs{
		ControllerToken: token,
		Samples:         samples,
	}
	if err := r.client.callWithTimeout(server.StreamPTTAudioRPC, args, nil); err != nil {
		r.lg.Debugf("StreamPTTAudio RPC failed (chunk dropped): %v", err)
	}
}

// Release is called when the PTT key transitions from down to up.
// Sends StopPTT only when the current press was granted.
func (r *PTTRelay) Release() {
	r.mu.Lock()
	wasGranted := r.granted
	r.pressed = false
	r.granted = false
	r.denied = false
	r.mu.Unlock()

	if !wasGranted {
		return
	}
	if err := r.client.callWithTimeout(server.StopPTTRPC, r.token, nil); err != nil {
		r.lg.Errorf("StopPTT RPC failed: %v", err)
	}
}

// IsDenied reports whether the current press was denied by the server.
// Returns false once Release is called.
func (r *PTTRelay) IsDenied() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.denied
}
