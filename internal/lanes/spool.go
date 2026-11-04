package lanes

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
)

// ErrSpoolLine says a line of a spool proves no lane, so it says nothing.
var ErrSpoolLine = errors.New("this spool line proves no lane")

const (
	spoolHookKey  = "hook"
	spoolSealKey  = "seal"
	spoolLaneKey  = "lane"
	spoolGenKey   = "generation"
	spoolSealText = "ropeshift spool line\x00"
)

// sealOf signs what a hook wrote with the digest of its lane's token. A worker holds that digest and
// not the token, and a spool holds neither, so a process that can write the file can say nothing of
// a lane whose token it never held.
func sealOf(digest string, hook []byte) string {
	mac := hmac.New(sha256.New, []byte(digest))
	mac.Write([]byte(spoolSealText))
	mac.Write(hook)
	return hex.EncodeToString(mac.Sum(nil))
}

// SealHook is one line of a spool. A seal stands where a token stands on the socket: the socket
// proves a lane on every call, and a spool is a file that anything this user runs can write. The line
// is built by hand, because a seal covers these exact bytes.
func SealHook(token string, event HookEvent) ([]byte, error) {
	if token == "" {
		return nil, fmt.Errorf("%w: no token", ErrSpoolLine)
	}
	hook, err := json.Marshal(event)
	if err != nil {
		return nil, err
	}
	line := append([]byte(`{"`+spoolHookKey+`":`), hook...)
	return append(line, []byte(`,"`+spoolSealKey+`":"`+sealOf(TokenDigest(token), hook)+`"}`)...), nil
}

// OpenHook reads one line of a spool, or says it proves nothing. digestOf gives the digest of a
// lane's token, and an empty one stands for a lane with none. Each field is read by its exact key,
// because a decoder that folds case would let a line show one lane to its seal and another to the
// worker, and the lane a seal proved is the one an event belongs to, whatever else the line claims.
func OpenHook(line []byte, digestOf func(lane string) (string, error)) (HookEvent, error) {
	var outer map[string]json.RawMessage
	if err := json.Unmarshal(line, &outer); err != nil {
		return HookEvent{}, fmt.Errorf("%w: %v", ErrSpoolLine, err)
	}
	hook := outer[spoolHookKey]
	var seal string
	if err := json.Unmarshal(outer[spoolSealKey], &seal); err != nil || len(hook) == 0 {
		return HookEvent{}, fmt.Errorf("%w: it holds no seal", ErrSpoolLine)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(hook, &fields); err != nil {
		return HookEvent{}, fmt.Errorf("%w: %v", ErrSpoolLine, err)
	}
	var lane string
	var generation int64
	if err := json.Unmarshal(fields[spoolLaneKey], &lane); err != nil || lane == "" {
		return HookEvent{}, fmt.Errorf("%w: it names no lane", ErrSpoolLine)
	}
	if err := json.Unmarshal(fields[spoolGenKey], &generation); err != nil {
		return HookEvent{}, fmt.Errorf("%w: it names no generation", ErrSpoolLine)
	}
	digest, err := digestOf(lane)
	if err != nil {
		return HookEvent{}, err
	}
	if digest == "" || !hmac.Equal([]byte(seal), []byte(sealOf(digest, hook))) {
		return HookEvent{}, fmt.Errorf("%w: its seal is not lane %s's", ErrSpoolLine, lane)
	}
	var event HookEvent
	if err := json.Unmarshal(hook, &event); err != nil {
		return HookEvent{}, fmt.Errorf("%w: %v", ErrSpoolLine, err)
	}
	if event.Event == "" {
		return HookEvent{}, fmt.Errorf("%w: it names no event", ErrSpoolLine)
	}
	event.Lane, event.Generation = lane, generation
	return event, nil
}
